package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func batchTargets(n int) []BatchTarget {
	targets := []BatchTarget{}
	for i := range n {
		name := string(rune('a' + i))
		targets = append(targets, BatchTarget{DeviceID: name, DeviceName: name, Request: DownloadRequest{
			Agent: nodeclient.Config{URL: "https://" + name + ":9443"}, SSH: transport.Config{Address: name + ":22"}, ShareID: "inbox-" + name,
		}})
	}
	return targets
}

func waitBatch(t *testing.T, p *BatchUploads, id string, predicate func(sqlite.UploadBatch) bool) sqlite.UploadBatch {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		items, err := p.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range items {
			if b.ID == id && predicate(b) {
				return b
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch condition timed out")
	return sqlite.UploadBatch{}
}

func TestBatchControlsJoinWritersAndKeepSharedSnapshotAcrossRestart(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "state.db")
	m := NewManager(database, nil)
	var started, stopped atomic.Int32
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		started.Add(1)
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond)
		stopped.Add(1)
		return engine.DownloadResult{}, ctx.Err()
	}
	p, err := NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(ctx) })
	if other, err := NewBatchUploads(ctx, database, m); err == nil {
		other.Close(ctx)
		t.Fatal("second queue owner accepted")
	}
	source := filepath.Join(t.TempDir(), "原始.bin")
	original := []byte("one immutable version")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 32)
	b, err := p.Create(ctx, key, source, batchTargets(3))
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(b.SnapshotDirectory, "objects", b.Manifest.ContentID)
	t.Cleanup(func() { _ = os.Chmod(object, 0600) })
	if b.SnapshotReferences != 3 {
		t.Fatal(b)
	}
	if err := os.WriteFile(source, []byte("changed after selecting"), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(ctx, key, source, batchTargets(3))
	if err != nil || again.Manifest.ManifestID != b.Manifest.ManifestID {
		t.Fatal("duplicate republished", again, err)
	}
	if data, _ := os.ReadFile(object); !bytes.Equal(data, original) {
		t.Fatal("snapshot changed")
	}
	changed := batchTargets(3)
	changed[0].Request.UploadTarget = "other.bin"
	if _, err := p.Create(ctx, key, source, changed); err == nil {
		t.Fatal("request key changed identity")
	}
	waitBatch(t, p, key, func(b sqlite.UploadBatch) bool { return started.Load() == 2 && b.Recipients[2].State == "queued" })
	if err := p.Control(ctx, key, "", "pause"); err != nil {
		t.Fatal(err)
	}
	b = waitBatch(t, p, key, func(b sqlite.UploadBatch) bool {
		return b.Recipients[0].State == "paused" && b.Recipients[1].State == "paused" && b.Recipients[2].State == "paused"
	})
	if stopped.Load() != 2 || b.SnapshotReferences != 3 {
		t.Fatal("writers or refs released early", stopped.Load(), b)
	}
	if err := p.Control(ctx, key, b.Recipients[0].TaskID, "cancel"); err != nil {
		t.Fatal(err)
	}
	b = waitBatch(t, p, key, func(b sqlite.UploadBatch) bool { return b.SnapshotReferences == 2 })
	if b.Recipients[1].State != "paused" {
		t.Fatal("individual cancel affected peer")
	}
	if err := p.Control(ctx, key, b.Recipients[1].TaskID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, p, key, func(b sqlite.UploadBatch) bool { return b.Recipients[1].State == "running" })
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stopped.Load() != 3 {
		t.Fatal("close left writer alive")
	}
	p, err = NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	b = waitBatch(t, p, key, func(b sqlite.UploadBatch) bool { return b.State == "paused" })
	if b.Recipients[0].State != "cancelled" || b.Recipients[1].State != "paused" || b.Recipients[2].State != "paused" || started.Load() != 3 {
		t.Fatal("reopen resent or lost state", b)
	}
	if err := p.Control(ctx, key, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	b = waitBatch(t, p, key, func(b sqlite.UploadBatch) bool { return b.SnapshotReferences == 0 })
	if _, err := os.Stat(object); err != nil {
		t.Fatal("zero-ref object must remain candidate", err)
	}
}

func TestBatchFailureIsIndependentAndCompletedRecipientNeverResends(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "tasks.db")
	m := NewManager(database, nil)
	var successCalls atomic.Int32
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		if r.ShareID == "inbox-a" {
			successCalls.Add(1)
			return engine.DownloadResult{}, nil
		}
		return engine.DownloadResult{}, errors.New("receiver denied")
	}
	p, err := NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	source := filepath.Join(t.TempDir(), "sample.bin")
	os.WriteFile(source, []byte("sample"), 0600)
	b, err := p.Create(ctx, strings.Repeat("b", 32), source, batchTargets(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(b.SnapshotDirectory, "objects", b.Manifest.ContentID), 0600) })
	b = waitBatch(t, p, b.ID, func(b sqlite.UploadBatch) bool {
		return b.Recipients[0].State == "completed" && b.Recipients[1].State == "failed"
	})
	if b.SnapshotReferences != 1 {
		t.Fatal("failed task lost snapshot")
	}
	if err := p.Control(ctx, b.ID, "", "resume"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, p, b.ID, func(b sqlite.UploadBatch) bool { return b.Recipients[1].State == "failed" })
	if successCalls.Load() != 1 {
		t.Fatal("completed recipient resent")
	}
}

func TestUploadManagerSharesFiniteBlockBudgetWithStandaloneTasks(t *testing.T) {
	ctx := context.Background()
	m := NewManager(filepath.Join(t.TempDir(), "tasks.db"), nil)
	defer m.Close(ctx)
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		<-ctx.Done()
		return engine.DownloadResult{}, ctx.Err()
	}
	makeRequest := func(name, node string, workers int) DownloadRequest {
		return DownloadRequest{ManifestID: strings.Repeat("a", 64), Destination: name, Direction: "upload", Workers: workers, SSH: transport.Config{Address: node}}
	}
	id, err := m.Start(ctx, makeRequest("first", "node-a:22", 2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, makeRequest("same-node", "node-a:22", 1)); !errors.Is(err, ErrUploadBudget) {
		t.Fatal("node budget", err)
	}
	if _, err = m.Start(ctx, makeRequest("second", "node-b:22", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, makeRequest("third", "node-c:22", 1)); !errors.Is(err, ErrUploadBudget) {
		t.Fatal("global budget", err)
	}
	if err = m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, makeRequest("released", "node-a:22", 2)); err != nil {
		t.Fatal("budget not released after joined stop", err)
	}
}

func TestBatchObjectReferencesIncludeOtherManifestsWithSameContent(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "state.db")
	m := NewManager(database, nil)
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		<-ctx.Done()
		return engine.DownloadResult{}, ctx.Err()
	}
	p, err := NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	var first, second sqlite.UploadBatch
	for i, name := range []string{"a.bin", "b.bin"} {
		source := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(source, []byte("shared content, different names"), 0600); err != nil {
			t.Fatal(err)
		}
		b, err := p.Create(ctx, strings.Repeat(string(rune('e'+i)), 32), source, batchTargets(1))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
		} else {
			second = b
		}
	}
	object := filepath.Join(first.SnapshotDirectory, "objects", first.Manifest.ContentID)
	t.Cleanup(func() { os.Chmod(object, 0600) })
	if first.Manifest.ManifestID == second.Manifest.ManifestID || first.Manifest.ContentID != second.Manifest.ContentID || second.SnapshotReferences != 2 {
		t.Fatal("cross-manifest content reference lost", first, second)
	}
	if err := p.Control(ctx, first.ID, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, p, first.ID, func(b sqlite.UploadBatch) bool { return b.SnapshotReferences == 1 })
	if err := p.Control(ctx, second.ID, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, p, second.ID, func(b sqlite.UploadBatch) bool { return b.SnapshotReferences == 0 })
}

func TestBatchPreparationRejectsExcessTargetsAndRecursiveSnapshotStore(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "state.db")
	m := NewManager(database, nil)
	p, err := NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(ctx)
	source := filepath.Join(t.TempDir(), "small.bin")
	if err := os.WriteFile(source, []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(ctx, strings.Repeat("f", 32), source, batchTargets(33)); err == nil {
		t.Fatal("excessive queue accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(database), "batch-content")); !os.IsNotExist(err) {
		t.Fatal("rejected queue prepared content")
	}
	if _, err := estimateBatchSnapshot(ctx, filepath.Dir(database), filepath.Join(filepath.Dir(database), "batch-content")); err == nil {
		t.Fatal("snapshot store inside selected folder")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	directory := t.TempDir()
	os.WriteFile(filepath.Join(directory, "child"), []byte("value"), 0600)
	if _, err := estimateBatchSnapshot(cancelled, directory, filepath.Join(filepath.Dir(database), "batch-content")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled preparation walked content", err)
	}
}
