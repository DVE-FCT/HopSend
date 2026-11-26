package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestUploadNestedChineseParentWithWriteOnlyPermission(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	parent := "项目/资料"
	if err := os.MkdirAll(filepath.Join(inbox, filepath.FromSlash(parent)), 0700); err != nil {
		t.Fatal(err)
	}
	r.UploadParentPath = parent
	r.Destination = app.UploadDestination(r.Agent.URL, r.ShareID, r.UploadTarget, parent)
	result, err := app.Upload(t.Context(), r)
	if err != nil {
		t.Fatal(result, err)
	}
	actual, err := os.ReadFile(filepath.Join(inbox, filepath.FromSlash(parent), r.UploadTarget))
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("nested upload bytes differ", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, r.UploadTarget)); !os.IsNotExist(err) {
		t.Fatal("upload silently fell back to root", err)
	}
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Entries(t.Context(), r.ShareID, parent); err == nil {
		t.Fatal("write-only permission became directory read")
	}
}

func TestUploadParentIdentityPersistsAcrossAgentRestart(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_parent", true: "replaced_parent"}[replace], func(t *testing.T) {
			inbox := filepath.Join(t.TempDir(), "inbox")
			if err := os.MkdirAll(filepath.Join(inbox, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			first := startAgentConfigured(t, func(cfg *httpapi.Config) {
				cfg.Shares = append(cfg.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
				cfg.Credentials[0].UploadShareIDs = []string{"inbox"}
			})
			source := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(source, []byte("restart"), 0600); err != nil {
				t.Fatal(err)
			}
			store := filepath.Join(t.TempDir(), "content")
			m, err := manifest.Publish(t.Context(), source, store, "inbox", manifest.MinChunkSize)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", m.ContentID), 0600) })
			session, err := first.client(t).CreateUpload(t.Context(), v1.CreateUpload{RequestID: strings.Repeat("c", 64), ShareID: "inbox", Target: "restart.txt", ParentPath: "nested", Manifest: m})
			if err != nil {
				t.Fatal(err)
			}
			first.server.Close()
			first.app.Close()
			if replace {
				if err := os.Rename(filepath.Join(inbox, "nested"), filepath.Join(inbox, "old")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(inbox, "nested"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			second := startAgentConfigured(t, func(cfg *httpapi.Config) { *cfg = first.config })
			second.ca = first.ca
			status, err := second.client(t).UploadStatus(t.Context(), session.ID)
			if replace {
				var detail *nodeclient.HTTPError
				if !errors.As(err, &detail) || detail.Code != "upload_parent_changed" {
					t.Fatal("restart accepted replacement", status, err)
				}
			} else if err != nil || status.ParentPath != "nested" {
				t.Fatal("restart lost binding", status, err)
			}
		})
	}
}

func TestNestedFolderAndEmptyFilePublishAtChosenLocation(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	if err := os.Mkdir(filepath.Join(inbox, "接收资料"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, isFolder := range []bool{true, false} {
		source := filepath.Join(t.TempDir(), "空文件.txt")
		target := "空文件.txt"
		if isFolder {
			source = filepath.Join(t.TempDir(), "源文件夹")
			target = "接收文件夹"
			if err := os.MkdirAll(filepath.Join(source, "空目录"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "中文.txt"), []byte("嵌套上传"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(source, nil, 0600); err != nil {
			t.Fatal(err)
		}
		request := r
		request.Database = filepath.Join(t.TempDir(), "tasks.db")
		request.UploadParentPath = "接收资料"
		request, err := app.PrepareUpload(t.Context(), request, source, target, manifest.MinChunkSize)
		if err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(request.SnapshotDirectory)
		if err != nil {
			t.Fatal(err)
		}
		m, err := manifest.Load(root, request.ManifestID)
		root.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(request.SnapshotDirectory, "objects", m.ContentID), 0600) })
		if _, err := app.Upload(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		if isFolder {
			data, err := os.ReadFile(filepath.Join(inbox, "接收资料", target, "中文.txt"))
			if err != nil || string(data) != "嵌套上传" {
				t.Fatal(string(data), err)
			}
			info, err := os.Stat(filepath.Join(inbox, "接收资料", target, "空目录"))
			if err != nil || !info.IsDir() {
				t.Fatal(err)
			}
		} else {
			info, err := os.Stat(filepath.Join(inbox, "接收资料", target))
			if err != nil || info.Size() != 0 {
				t.Fatal(err)
			}
		}
		if _, err := app.Upload(t.Context(), request); err != nil {
			t.Fatal("completed session cannot reconcile chosen parent", err)
		}
	}
}

func TestOldAgentRefusesNestedUploadBeforeSnapshotOrPost(t *testing.T) {
	f := startAgent(t)
	posts := 0
	old := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v1.Capabilities{APIVersion: "v1", Operations: []string{"create_upload"}, DataTransport: "external_sftp", UploadSupported: true})
			return
		}
		if r.Method == "POST" {
			posts++
		}
		f.app.HTTP.Handler.ServeHTTP(w, r)
	}))
	old.TLS = f.server.TLS.Clone()
	old.StartTLS()
	defer old.Close()
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "tasks.db")
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: old.URL, CACertificate: f.ca, TokenFile: f.token}, ShareID: "files", Database: database, UploadParentPath: "nested"}
	if _, err := app.PrepareUpload(t.Context(), r, source, "target.txt", manifest.MinChunkSize); err == nil {
		t.Fatal("old Agent accepted parent path")
	}
	if posts != 0 {
		t.Fatal("unsupported nested upload issued POST")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(database), "upload-content")); !os.IsNotExist(err) {
		t.Fatal("unsupported upload copied source before capability check")
	}
}

func TestNestedUploadResumeKeepsBoundParent(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	if err := os.Mkdir(filepath.Join(inbox, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	r.UploadParentPath = "nested"
	r.Destination = app.UploadDestination(r.Agent.URL, r.ShareID, r.UploadTarget, "nested")
	manager := app.NewManager(r.Database, nil)
	ctx, cancel := context.WithCancel(t.Context())
	r.OnProgress = func(p engine.Progress) {
		if p.State == "uploading" && p.CompletedChunks > 0 {
			cancel()
		}
	}
	id, err := manager.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Wait(t.Context(), id)
	if err == nil {
		t.Fatal("planned interruption did not stop upload")
	}
	manager.Close(t.Context())
	restarted := app.NewManager(r.Database, nil)
	defer restarted.Close(t.Context())
	if _, err := restarted.Resume(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Wait(t.Context(), id)
	if err != nil {
		t.Fatal(result, err)
	}
	actual, err := os.ReadFile(filepath.Join(inbox, "nested", r.UploadTarget))
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("resumed nested upload differs", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, r.UploadTarget)); !os.IsNotExist(err) {
		t.Fatal("resume fell back to root")
	}
}

func TestUploadParentRejectsTraversalMissingAndJunction(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(root, r.ManifestID)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	request := v1.CreateUpload{RequestID: strings.Repeat("f", 64), ShareID: r.ShareID, Target: "nested.bin", Manifest: m}
	for _, parent := range []string{"../escape", "C:/escape", "a\\b", "a//b", "CON", "name.", ".hopsend-uploads/session", "missing"} {
		request.ParentPath = parent
		if _, err := c.CreateUpload(t.Context(), request); err == nil {
			t.Fatal("unsafe/missing parent accepted", parent)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(inbox, "link")
	err = os.Symlink(outside, link)
	if err != nil && runtime.GOOS == "windows" {
		_, err = exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, outside).CombinedOutput()
	}
	if err != nil {
		t.Skip("link creation unavailable", err)
	}
	request.ParentPath = "link"
	if _, err := c.CreateUpload(t.Context(), request); err == nil {
		t.Fatal("junction upload parent accepted")
	}
}

func TestLegacyUploadSessionDoesNotAcquireImplicitParentTrust(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(root, r.ManifestID)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	request := v1.CreateUpload{RequestID: strings.Repeat("e", 64), ShareID: r.ShareID, Target: "legacy.bin", Manifest: m}
	session, err := c.CreateUpload(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the migrated v2 row's absent binding without changing any bytes.
	database := filepath.Join(filepath.Dir(r.Agent.TokenFile), "state", "agent.db")
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE uploads SET parent_identity='' WHERE id=?", session.ID)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	var detail *nodeclient.HTTPError
	if _, err := c.UploadStatus(t.Context(), session.ID); !errors.As(err, &detail) || detail.Code != "upload_parent_unbound" {
		t.Fatal("legacy session silently rebound", err)
	}
	if _, err := c.CreateUpload(t.Context(), request); !errors.As(err, &detail) || detail.Code != "upload_parent_unbound" {
		t.Fatal("legacy retry silently rebound", err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "legacy.bin")); !os.IsNotExist(err) {
		t.Fatal("legacy session published without binding")
	}
}

func TestUploadParentReplacementRejectsStatusFinalizeAndIdempotentRetry(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	parent := filepath.Join(inbox, "destination")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(root, r.ManifestID)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	request := v1.CreateUpload{RequestID: strings.Repeat("c", 64), ShareID: r.ShareID, Target: r.UploadTarget, ParentPath: "destination", Manifest: m}
	session, err := c.CreateUpload(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	var detail *nodeclient.HTTPError
	for _, operation := range []func() error{
		func() error { _, err := c.UploadStatus(t.Context(), session.ID); return err },
		func() error { _, err := c.FinalizeUpload(t.Context(), session.ID); return err },
		func() error { _, err := c.CreateUpload(t.Context(), request); return err },
	} {
		if err := operation(); !errors.As(err, &detail) || detail.Code != "upload_parent_changed" {
			t.Fatal("changed parent accepted", err)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, r.UploadTarget)); !os.IsNotExist(err) {
		t.Fatal("published into replacement parent", err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent+"-original", parent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UploadStatus(t.Context(), session.ID); err != nil {
		t.Fatal("original directory cannot resume", err)
	}
}
