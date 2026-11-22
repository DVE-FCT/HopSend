package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func uploadFixture(t *testing.T) (app.DownloadRequest, []byte, string) {
	t.Helper()
	ssh, data, _ := startSFTP(t, 0)
	dir := filepath.Dir(ssh.PrivateKeyPath)
	inbox := filepath.Join(dir, "inbox")
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) {
		cfg.Shares = append(cfg.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
		cfg.Credentials[0].UploadShareIDs = []string{"inbox"}
	})
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: "inbox", Database: filepath.Join(t.TempDir(), "state.db"), Workers: 2, ReadTimeout: time.Second}
	source := filepath.Join(t.TempDir(), "上传样本.bin")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := app.PrepareUpload(context.Background(), r, source, "received.bin", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		root, _ := os.OpenRoot(r.SnapshotDirectory)
		if root != nil {
			m, e := manifest.Load(root, r.ManifestID)
			if e == nil {
				_ = os.Chmod(filepath.Join(r.SnapshotDirectory, "objects", m.ContentID), 0600)
			}
			_ = root.Close()
		}
	})
	return r, data, inbox
}

type lostUploadACK struct {
	*nodeclient.Client
	once sync.Once
}

func (r *lostUploadACK) ConfirmUpload(ctx context.Context, id string, index int) (v1.UploadSession, error) {
	value, err := r.Client.ConfirmUpload(ctx, id, index)
	if err != nil {
		return value, err
	}
	lost := false
	r.once.Do(func() { lost = true })
	if lost {
		return value, &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}
	}
	return value, nil
}

func TestReceiverConfirmedChunkSurvivesLostAcknowledgement(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	ctx := context.Background()
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, _ := os.OpenRoot(r.SnapshotDirectory)
	m, err := manifest.Load(root, r.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	session, err := c.CreateUpload(ctx, v1.CreateUpload{RequestID: engine.TaskID(m.ManifestID, r.Destination), ShareID: r.ShareID, Target: r.UploadTarget, Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(r.Database)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(r)
	_, err = engine.Upload(ctx, engine.UploadOptions{Manifest: m, Snapshot: filepath.Join(r.SnapshotDirectory, "objects", m.ContentID), Destination: r.Destination, LockPath: filepath.Join(t.TempDir(), "writer.lock"), Session: session, Workers: 1, Receiver: &lostUploadACK{Client: c}, Journal: store, Source: source, OpenSink: func(ctx context.Context) (engine.RangeSink, error) { return transport.Dial(ctx, r.SSH) }})
	_ = store.Close()
	if err == nil {
		t.Fatal("lost acknowledgement ignored")
	}
	status, err := c.UploadStatus(ctx, session.ID)
	if err != nil || len(status.Confirmed) != 1 {
		t.Fatal("receiver confirmation lost", status, err)
	}
	result, err := app.Resume(ctx, r.Database, engine.TaskID(m.ManifestID, r.Destination), nil)
	if err != nil || result.ReusedChunks != 1 {
		t.Fatal("lost ACK did not reconcile receiver record", result, err)
	}
	actual, _ := os.ReadFile(filepath.Join(inbox, r.UploadTarget))
	if !bytes.Equal(actual, data) {
		t.Fatal("lost ACK recovery data differs")
	}
}

func TestReceiverRestartReconcilesPublicationBetweenLinkAndCommit(t *testing.T) {
	ssh, data, _ := startSFTP(t, 0)
	inbox := filepath.Join(filepath.Dir(ssh.PrivateKeyPath), "inbox")
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(c *httpapi.Config) {
		c.Shares = append(c.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
		c.Credentials[0].UploadShareIDs = []string{"inbox"}
	})
	c := agent.client(t)
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(t.TempDir(), "content")
	m, err := manifest.Publish(ctx, source, content, "inbox", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(content, "objects", m.ContentID), 0600) })
	session, err := c.CreateUpload(ctx, v1.CreateUpload{RequestID: strings.Repeat("d", 64), ShareID: "inbox", Target: "restart.bin", Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(filepath.Dir(agent.config.KeyFile), "state", "agent.db")
	store, err := sqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Upload(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(inbox, record.Staging), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range m.Chunks {
		if _, err = c.ConfirmUpload(ctx, session.ID, chunk.Index); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.SetUploadState(ctx, session.ID, "publishing"); err != nil {
		t.Fatal(err)
	}
	share, err := os.OpenRoot(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if err = share.Link(record.Staging, record.Target); err != nil {
		t.Fatal(err)
	}
	_ = share.Close()
	_ = store.Close()
	// Recreate the receiving server and its DB connection, with no cached session.
	agent.server.Close()
	_ = agent.app.Close()
	srv, err := httpapi.New(agent.config)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	pair, err := tls.LoadX509KeyPair(agent.config.CertificateFile, agent.config.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv.HTTP.Handler)
	ts.TLS = srv.HTTP.TLSConfig.Clone()
	ts.TLS.Certificates = []tls.Certificate{pair}
	ts.StartTLS()
	defer ts.Close()
	restarted, err := nodeclient.New(nodeclient.Config{URL: ts.URL, CACertificate: agent.ca, TokenFile: agent.token})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	status, err := restarted.UploadStatus(ctx, session.ID)
	if err != nil || status.State != "completed" || status.SHA256 != m.ContentID {
		t.Fatal("publication not reconciled", status, err)
	}
	if _, err = os.Stat(filepath.Join(inbox, record.Staging)); !os.IsNotExist(err) {
		t.Fatal("staging link retained")
	}
}

func TestUploadSessionIsBoundToAuthenticatedOwner(t *testing.T) {
	ssh, _, _ := startSFTP(t, 0)
	inbox := filepath.Join(filepath.Dir(ssh.PrivateKeyPath), "inbox")
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	otherToken := filepath.Join(t.TempDir(), "other.token")
	newToken := make([]byte, 32)
	if _, err := rand.Read(newToken); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherToken, []byte(hex.EncodeToString(newToken)), 0600); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(c *httpapi.Config) {
		c.Shares = append(c.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
		c.Credentials[0].UploadShareIDs = []string{"inbox"}
		c.Credentials = append(c.Credentials, httpapi.Credential{ID: "another-device", TokenFile: otherToken, ShareIDs: []string{"inbox"}, UploadShareIDs: []string{"inbox"}})
	})
	config := nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}
	first, err := nodeclient.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	config.TokenFile = otherToken
	second, err := nodeclient.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	source := filepath.Join(t.TempDir(), "source.bin")
	if err = os.WriteFile(source, []byte("owner bound"), 0600); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(t.TempDir(), "content")
	m, err := manifest.Publish(context.Background(), source, content, "inbox", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(content, "objects", m.ContentID), 0600) })
	request := v1.CreateUpload{RequestID: strings.Repeat("a", 64), ShareID: "inbox", Target: "owner.bin", Manifest: m}
	session, err := first.CreateUpload(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.UploadStatus(context.Background(), session.ID)
	var status *nodeclient.HTTPError
	if !errors.As(err, &status) || status.StatusCode != 404 {
		t.Fatal("cross-owner session exposed", err)
	}
	request.Target = "different.bin"
	if _, err = first.CreateUpload(context.Background(), request); err == nil {
		t.Fatal("request identity rebound to another target")
	}
}

func TestReceiverCapacityRejectsBeforeAllocatingHugeFile(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	available, err := filesystem.AvailableBytes(inbox)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(manifest.MaxChunkSize) * int64(manifest.MaxChunks)
	if available >= uint64(size) {
		t.Skip("fixture needs a volume smaller than maximum manifest size")
	}
	chunks := make([]manifestv1.Chunk, manifest.MaxChunks)
	for i := range chunks {
		chunks[i] = manifestv1.Chunk{Index: i, Offset: int64(i) * manifest.MaxChunkSize, Length: manifest.MaxChunkSize, SHA256: strings.Repeat("a", 64)}
	}
	m := manifestv1.Manifest{SchemaVersion: 1, ContentID: strings.Repeat("a", 64), ShareID: "inbox", Name: "huge.bin", SizeBytes: size, ChunkSizeBytes: manifest.MaxChunkSize, Chunks: chunks}
	m.ManifestID = manifest.ID(m)
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.CreateUpload(context.Background(), v1.CreateUpload{RequestID: strings.Repeat("c", 64), ShareID: "inbox", Target: "huge.bin", Manifest: m})
	var status *nodeclient.HTTPError
	if !errors.As(err, &status) || status.StatusCode != 507 {
		t.Fatal("capacity failure not rejected before allocation", err)
	}
	if _, err = os.Stat(filepath.Join(inbox, "huge.bin")); !os.IsNotExist(err) {
		t.Fatal("huge target created")
	}
}

func TestActualHTTPSReceiverConfirmsSFTPUploadAndFinalHash(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	var states []engine.Progress
	r.OnProgress = func(p engine.Progress) { states = append(states, p) }
	result, err := app.Upload(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(inbox, "received.bin"))
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("receiver bytes differ", err)
	}
	if len(states) == 0 || states[len(states)-1].State != "completed" {
		t.Fatal(states)
	}
	store, err := sqlite.Open(r.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task, err := store.Get(context.Background(), result.TaskID)
	if err != nil || task.State != "completed" || len(task.Confirmed) != len(task.Manifest.Chunks) {
		t.Fatal(task.State, err)
	}
	var saved app.DownloadRequest
	if err = json.Unmarshal(task.Source, &saved); err != nil || saved.Direction != "upload" {
		t.Fatal(saved, err)
	}
	// An idempotent resume reconciles receiver state; it never overwrites data.
	result, err = app.Resume(context.Background(), r.Database, result.TaskID, nil)
	if err != nil || result.ReusedChunks != len(task.Manifest.Chunks) {
		t.Fatal(result, err)
	}
}

func TestReceiverRejectsUnconfirmedOrCorruptUploadAndRepairsResume(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	ctx := context.Background()
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	m, err := manifest.Load(root, r.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	request := v1.CreateUpload{RequestID: engine.TaskID(m.ManifestID, r.Destination), ShareID: r.ShareID, Target: r.UploadTarget, Manifest: m}
	session, err := c.CreateUpload(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.FinalizeUpload(ctx, session.ID); err == nil {
		t.Fatal("unconfirmed upload published")
	}
	if _, err = c.ConfirmUpload(ctx, session.ID, 0); err == nil {
		t.Fatal("unwritten block accepted")
	}
	// Seed one actual receiver block, confirm it, then damage its disk bytes.
	staging := filepath.FromSlash(strings.TrimPrefix(session.SFTPPath, "/"))
	if filepath.IsAbs(session.SFTPPath) {
		if !strings.Contains(staging, ":") {
			staging = session.SFTPPath
		}
	}
	f, err := os.OpenFile(staging, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(data[:m.Chunks[0].Length], 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	ack, err := c.ConfirmUpload(ctx, session.ID, 0)
	if err != nil || len(ack.Confirmed) != 1 {
		t.Fatal(ack, err)
	}
	f, err = os.OpenFile(staging, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte("bad"), 0)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.UploadStatus(ctx, session.ID)
	if err != nil || len(status.Confirmed) != 0 {
		t.Fatal("receiver trusted corrupt confirmed block", status, err)
	}
	if _, err = app.Upload(ctx, r); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(filepath.Join(inbox, "received.bin"))
	if !bytes.Equal(actual, data) {
		t.Fatal("repair differed")
	}
	// Read-only share authorization remains distinct from writable inbox.
	request.ShareID = "files"
	request.Manifest.ShareID = "files"
	request.Manifest.ManifestID = manifest.ID(request.Manifest)
	request.RequestID = strings.Repeat("a", 64)
	_, err = c.CreateUpload(ctx, request)
	var httpErr *nodeclient.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 403 {
		t.Fatal("read-only share accepted upload", err)
	}
}

func TestUploadActualConnectionLossUsesReceiverConfirmedBlocks(t *testing.T) {
	ssh, data, _, drop := startSFTPControlled(t, 0)
	inbox := filepath.Join(filepath.Dir(ssh.PrivateKeyPath), "inbox")
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(c *httpapi.Config) {
		c.Shares = append(c.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
		c.Credentials[0].UploadShareIDs = []string{"inbox"}
	})
	source := filepath.Join(t.TempDir(), "network.bin")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: "inbox", Database: filepath.Join(t.TempDir(), "tasks.db"), Workers: 2, Retries: 4, RetryDelay: time.Millisecond, ReadTimeout: time.Second}
	r, err := app.PrepareUpload(context.Background(), r, source, "network.bin", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := os.OpenRoot(r.SnapshotDirectory)
	m, err := manifest.Load(root, r.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(r.SnapshotDirectory, "objects", m.ContentID), 0600) })
	var once sync.Once
	waited := false
	r.OnProgress = func(p engine.Progress) {
		if p.CompletedChunks > 0 && p.State == "uploading" {
			once.Do(drop)
		}
		if p.State == "waiting_network" {
			waited = true
		}
	}
	result, err := app.Upload(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(inbox, "network.bin"))
	if err != nil || !bytes.Equal(actual, data) || !waited || result.ReusedChunks == 0 {
		t.Fatal("upload did not restore receiver confirmations", result, waited, err)
	}
}

func TestReceiverChecksWholeHashAndNeverOverwritesTarget(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	ctx := context.Background()
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, _ := os.OpenRoot(r.SnapshotDirectory)
	m, err := manifest.Load(root, r.ManifestID)
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	m.ContentID = strings.Repeat("f", 64)
	m.ManifestID = manifest.ID(m)
	session, err := c.CreateUpload(ctx, v1.CreateUpload{RequestID: strings.Repeat("e", 64), ShareID: "inbox", Target: "bad-final.bin", Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.FromSlash(strings.TrimPrefix(session.SFTPPath, "/"))
	if !strings.Contains(staging, ":") {
		staging = session.SFTPPath
	}
	if err = os.WriteFile(staging, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range m.Chunks {
		if _, err = c.ConfirmUpload(ctx, session.ID, chunk.Index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = c.FinalizeUpload(ctx, session.ID); err == nil {
		t.Fatal("invalid full-file hash published")
	}
	if _, err = os.Stat(filepath.Join(inbox, "bad-final.bin")); !os.IsNotExist(err) {
		t.Fatal("bad final file exists")
	}
	if err = os.WriteFile(filepath.Join(inbox, r.UploadTarget), []byte("preserve user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Upload(ctx, r); err == nil {
		t.Fatal("existing target overwritten")
	}
	actual, _ := os.ReadFile(filepath.Join(inbox, r.UploadTarget))
	if string(actual) != "preserve user data" {
		t.Fatal("target changed")
	}
}
