package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestBatchActualTLSAndSFTPShareOneSnapshotWithIndependentReceivers(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "tasks.db")
	targets := []app.BatchTarget{}
	inboxes := []string{}
	for i, share := range []string{"inbox-a", "different-b", "denied-c", "conflict-d", "offline-e"} {
		ssh, _, _ := startSFTP(t, 0)
		inbox := filepath.Join(filepath.Dir(ssh.PrivateKeyPath), "inbox")
		if err := os.MkdirAll(filepath.Join(inbox, "资料"), 0700); err != nil {
			t.Fatal(err)
		}
		agent := startAgentConfigured(t, func(c *httpapi.Config) {
			c.Shares = append(c.Shares, httpapi.Share{ID: share, Path: inbox, Writable: true})
			if i != 2 {
				c.Credentials[0].UploadShareIDs = []string{share}
			}
		})
		if i == 4 {
			agent.server.Close()
		}
		inboxes = append(inboxes, inbox)
		targets = append(targets, app.BatchTarget{DeviceID: share, DeviceName: share, Request: app.DownloadRequest{
			Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: share, UploadParentPath: "资料", ReadTimeout: time.Second,
		}})
	}
	source := filepath.Join(t.TempDir(), "相同版本.bin")
	original := bytes.Repeat([]byte("batch fixed bytes"), 65536)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inboxes[3], "资料", filepath.Base(source)), []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	m := app.NewManager(database, nil)
	p, err := app.NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Close(ctx); m.Close(ctx) }()
	b, err := p.Create(ctx, strings.Repeat("c", 32), source, targets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(b.SnapshotDirectory, "objects", b.Manifest.ContentID), 0600) })
	if err := os.WriteFile(source, []byte("changed source after snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		all, e := p.List(ctx)
		if e != nil {
			t.Fatal(e)
		}
		b = all[0]
		if b.Recipients[0].State == "completed" && b.Recipients[1].State == "completed" && b.Recipients[2].State == "failed" && b.Recipients[3].State == "failed" && b.Recipients[4].State == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, r := range b.Recipients {
		want := "completed"
		if i > 1 {
			want = "failed"
		}
		if r.State != want {
			t.Fatalf("receiver %d: %+v", i, r)
		}
		var request app.DownloadRequest
		if err := json.Unmarshal(r.Request, &request); err != nil {
			t.Fatal(err)
		}
		if request.ManifestID != b.Manifest.ManifestID || request.SnapshotDirectory != b.SnapshotDirectory || request.UploadManifestScope != "source_snapshot" {
			t.Fatal("snapshot duplicated or rewritten")
		}
	}
	if b.SnapshotReferences != 3 {
		t.Fatal("failed receivers need recoverable snapshot", b.SnapshotReferences)
	}
	for i := range 2 {
		actual, err := os.ReadFile(filepath.Join(inboxes[i], "资料", filepath.Base(source)))
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatal("receiver did not get frozen bytes", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(inboxes[2], "资料", filepath.Base(source))); !os.IsNotExist(err) {
		t.Fatal("unauthorized destination written")
	}
	if actual, _ := os.ReadFile(filepath.Join(inboxes[3], "资料", filepath.Base(source))); string(actual) != "do not overwrite" {
		t.Fatal("conflicting file replaced")
	}
	if err := p.Control(ctx, b.ID, "", "cancel"); err != nil {
		t.Fatal(err)
	}
	all, err := p.List(ctx)
	if err != nil || all[0].SnapshotReferences != 0 {
		t.Fatal(all, err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	p, err = app.NewBatchUploads(ctx, database, m)
	if err != nil {
		t.Fatal(err)
	}
	all, err = p.List(ctx)
	if err != nil || all[0].Recipients[0].State != "completed" || all[0].Recipients[2].State != "cancelled" {
		t.Fatal("recovery changed terminal recipients", all, err)
	}
}

func TestSnapshotScopeDoesNotGrantDestinationSharePermission(t *testing.T) {
	r, _, _ := uploadFixture(t)
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
	m.ShareID = "source-snapshot"
	m.ManifestID = manifest.ID(m)
	request := v1.CreateUpload{RequestID: strings.Repeat("e", 64), ShareID: "inbox", Target: "isolated.bin", Manifest: m}
	if _, err := c.CreateUpload(ctx, request); err == nil {
		t.Fatal("default share mismatch allowed")
	}
	request.ManifestScope = "source_snapshot"
	session, err := c.CreateUpload(ctx, request)
	if err != nil || session.ManifestScope != "source_snapshot" {
		t.Fatal(session, err)
	}
	request.ShareID = "files"
	request.RequestID = strings.Repeat("f", 64)
	if _, err := c.CreateUpload(ctx, request); err == nil {
		t.Fatal("source namespace granted target permission")
	}
	status, err := c.UploadStatus(ctx, session.ID)
	if err != nil || status.ManifestScope != "source_snapshot" {
		t.Fatal("scope lost in persistence", status, err)
	}
}

func TestSnapshotClientRefusesUnsupportedCapabilityAndChangedScope(t *testing.T) {
	f := startAgent(t)
	for _, scenario := range []string{"old", "wrong-version", "wrong-scope", "unknown-status-scope"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/capabilities" {
					caps := v1.Capabilities{APIVersion: "v1", Operations: []string{"upload_source_snapshot"}, DataTransport: "external_sftp", UploadSupported: true}
					if scenario == "old" {
						caps.Operations = nil
					}
					if scenario == "wrong-version" {
						caps.APIVersion = "v2"
					}
					json.NewEncoder(w).Encode(caps)
					return
				}
				if r.Method == "POST" {
					posts++
				}
				json.NewEncoder(w).Encode(v1.UploadSession{ID: strings.Repeat("a", 32), RequestID: strings.Repeat("b", 64), ShareID: "inbox", Target: "t.bin", ManifestID: strings.Repeat("c", 64), SFTPPath: "/staging", ManifestScope: map[bool]string{true: "unknown"}[scenario == "unknown-status-scope"]})
			}))
			server.TLS = f.server.TLS.Clone()
			server.StartTLS()
			defer server.Close()
			c, err := nodeclient.New(nodeclient.Config{URL: server.URL, CACertificate: f.ca, TokenFile: f.token})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if scenario == "unknown-status-scope" {
				if _, err := c.UploadStatus(t.Context(), strings.Repeat("a", 32)); err == nil {
					t.Fatal("unknown status scope accepted")
				}
				return
			}
			request := v1.CreateUpload{RequestID: strings.Repeat("b", 64), ShareID: "inbox", Target: "t.bin", ManifestScope: "source_snapshot"}
			request.Manifest.ManifestID = strings.Repeat("c", 64)
			if _, err := c.CreateUpload(t.Context(), request); err == nil {
				t.Fatal("unsupported or changed scope accepted")
			}
			if scenario != "wrong-scope" && posts != 0 {
				t.Fatal("unsupported mode issued POST")
			}
		})
	}
}
