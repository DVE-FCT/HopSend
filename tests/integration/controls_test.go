package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

func controlledDownloadFixture(t *testing.T) (app.DownloadRequest, []byte, func()) {
	t.Helper()
	ssh, data, _, drop := startSFTPControlled(t, 40*time.Millisecond)
	dir := filepath.Dir(ssh.PrivateKeyPath)
	content := filepath.Join(dir, "content")
	m, err := manifest.Publish(context.Background(), filepath.Join(dir, "sample.bin"), content, "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(content, "objects", m.ContentID), 0600) })
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) { cfg.ContentDirectory = content })
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: "files", ManifestID: m.ManifestID, Destination: filepath.Join(t.TempDir(), "out.bin"), Database: filepath.Join(t.TempDir(), "tasks.sqlite"), Workers: 2, ReadTimeout: 2 * time.Second, Retries: 4, RetryDelay: 20 * time.Millisecond}
	return r, data, drop
}

func TestManagerActualSFTPPauseResumeAndCancel(t *testing.T) {
	r, data, _ := controlledDownloadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ready := make(chan struct{})
	var once sync.Once
	m := app.NewManager(r.Database, func(s app.TaskStatus) {
		if s.CompletedChunks > 0 && s.State == "downloading" {
			once.Do(func() { close(ready) })
		}
	})
	id, err := m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("no verified progress")
	}
	if err = m.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(r.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before, err := store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "paused" || len(before.Confirmed) == 0 || len(before.Confirmed) == len(before.Manifest.Chunks) {
		t.Fatal(before.State, before.Confirmed)
	}
	partial, err := os.ReadFile(before.Staging)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	partialAfter, err := os.ReadFile(before.Staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Confirmed) != len(after.Confirmed) || !bytes.Equal(partial, partialAfter) {
		t.Fatal("old writer advanced after Pause returned")
	}
	if _, err = m.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	result, err := m.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("resumed real download differs", err)
	}
	r.Destination = filepath.Join(t.TempDir(), "cancelled.bin")
	id, err = m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Wait(ctx, id); !errors.Is(err, app.ErrCancelled) {
		t.Fatal(err)
	}
	if _, err = os.Stat(r.Destination); !os.IsNotExist(err) {
		t.Fatal("cancel published destination")
	}
	status, _ := m.Status(id)
	if status.State != "cancelled" {
		t.Fatal(status)
	}
}

func TestActualSFTPConnectionDropRetriesAfterWorkersExit(t *testing.T) {
	r, data, drop := controlledDownloadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := make(chan struct{})
	waiting := make(chan struct{})
	var firstOnce, waitingOnce sync.Once
	m := app.NewManager(r.Database, func(s app.TaskStatus) {
		if s.CompletedChunks > 0 && s.State == "downloading" {
			firstOnce.Do(func() { close(first) })
		}
		if s.State == "waiting_network" {
			waitingOnce.Do(func() { close(waiting) })
		}
	})
	id, err := m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("no SFTP bytes")
	}
	drop()
	select {
	case <-waiting:
	case <-ctx.Done():
		status, _ := m.Status(id)
		t.Fatal("actual broken TCP connection did not enter waiting_network", status)
	}
	result, err := m.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("network recovery differs", err)
	}
	if result.ReusedChunks == 0 {
		t.Fatal("confirmed blocks not reused")
	}
}
