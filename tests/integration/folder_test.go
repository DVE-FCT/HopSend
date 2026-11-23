package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/folder"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestFolderRealUploadDownloadEmptyDirectoriesAndChinesePaths(t *testing.T) {
	ctx := context.Background()
	ssh, _, _ := startSFTP(t, 0)
	dir := filepath.Dir(ssh.PrivateKeyPath)
	source := filepath.Join(t.TempDir(), "中文源目录")
	long := strings.Repeat("较长目录", 8) + "/" + strings.Repeat("中文子目录", 8) + "/" + strings.Repeat("最后目录", 8)
	for _, path := range []string{"空目录", long, "混合/空子目录"} {
		if err := os.MkdirAll(filepath.Join(source, filepath.FromSlash(path)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"空文件": nil, "混合/文本.txt": []byte("HopSend 中文文件夹"), long + "/内容.bin": bytes.Repeat([]byte("真实字节"), 50000)}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(path)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	content := filepath.Join(dir, "content")
	m, err := app.Publish(ctx, source, content, "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	if m.PayloadKind != "folder" {
		t.Fatal("folder payload not declared")
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(content, "objects", m.ContentID), 0600) })
	inbox := filepath.Join(dir, "inbox")
	if err = os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(c *httpapi.Config) {
		c.ContentDirectory = content
		c.Shares = append(c.Shares, httpapi.Share{ID: "inbox", Path: inbox, Writable: true})
		c.Credentials[0].UploadShareIDs = []string{"inbox"}
	})
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: "files", ManifestID: m.ManifestID, Destination: filepath.Join(t.TempDir(), "下载目录"), Database: filepath.Join(t.TempDir(), "download.db"), Workers: 2}
	result, err := app.Download(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Verify(ctx, filepath.Join(content, "objects", m.ContentID), result.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Resume(ctx, r.Database, result.TaskID, nil); err != nil {
		t.Fatal("folder complete reconciliation", err)
	}
	r.ShareID = "inbox"
	r.Database = filepath.Join(t.TempDir(), "upload.db")
	r, err = app.PrepareUpload(ctx, r, source, "接收目录", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := os.OpenRoot(r.SnapshotDirectory)
	uploadManifest, err := manifest.Load(root, r.ManifestID)
	_ = root.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(r.SnapshotDirectory, "objects", uploadManifest.ContentID), 0600) })
	result, err = app.Upload(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Verify(ctx, filepath.Join(r.SnapshotDirectory, "objects", uploadManifest.ContentID), filepath.Join(inbox, "接收目录")); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Resume(ctx, r.Database, result.TaskID, nil); err != nil {
		t.Fatal("receiver folder reconciliation", err)
	}
}
