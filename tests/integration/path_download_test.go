package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/publication"
)

func TestSelectedPathVersionDownloadsOriginalBytesAfterRepublish(t *testing.T) {
	ssh, original, _ := startSFTP(t, 0)
	root := filepath.Dir(ssh.PrivateKeyPath)
	store := filepath.Join(root, "content")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) {
		cfg.ContentDirectory = store
		cfg.Shares[0] = httpapi.Share{ID: "chosen", Path: root}
		cfg.Credentials[0].ShareIDs = []string{"chosen"}
	})
	first, err := publication.Publish(t.Context(), root, store, "chosen", "sample.bin", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}
	selected := app.ResolvePathVersion(t.Context(), node, "chosen", "sample.bin")
	if selected.Error != nil || selected.Version == nil || selected.Version.ManifestID != first.ManifestID {
		t.Fatal(selected)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.bin"), bytes.Repeat([]byte("z"), len(original)), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := publication.Publish(t.Context(), root, store, "chosen", "sample.bin", manifest.MinChunkSize)
	if err != nil || second.ContentID == first.ContentID {
		t.Fatal(second, err)
	}
	request := app.DownloadRequest{Agent: node, SSH: ssh, ShareID: selected.Version.ShareID, ManifestID: selected.Version.ManifestID, Destination: filepath.Join(t.TempDir(), "selected.bin"), Workers: 2, Database: filepath.Join(t.TempDir(), "state.db")}
	result, err := app.Download(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("selected version changed after republish", err)
	}
	for _, id := range []string{first.ContentID, second.ContentID} {
		id := id
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", id), 0600) })
	}
}

func TestSelectedPathTaskResumesOriginalManifestAfterRestart(t *testing.T) {
	ssh, original, started := startSFTP(t, 5*time.Millisecond)
	root := filepath.Dir(ssh.PrivateKeyPath)
	store := filepath.Join(root, "content")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatal(err)
	}
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) { cfg.ContentDirectory = store; cfg.Shares[0].Path = root })
	first, err := publication.Publish(t.Context(), root, store, "files", "sample.bin", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}
	selected := app.ResolvePathVersion(t.Context(), node, "files", "sample.bin")
	if selected.Error != nil {
		t.Fatal(selected)
	}
	database := filepath.Join(t.TempDir(), "state.db")
	manager := app.NewManager(database, nil)
	id, err := manager.Start(t.Context(), app.DownloadRequest{Agent: node, SSH: ssh, ShareID: "files", ManifestID: selected.Version.ManifestID, Destination: filepath.Join(t.TempDir(), "resumed.bin"), Workers: 1, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("transfer did not start")
	}
	if err := manager.Pause(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.bin"), bytes.Repeat([]byte("n"), len(original)), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := publication.Publish(t.Context(), root, store, "files", "sample.bin", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	restarted := app.NewManager(database, nil)
	defer restarted.Close(t.Context())
	resumedID, err := restarted.Resume(t.Context(), id)
	if err != nil || resumedID != id {
		t.Fatal(resumedID, err)
	}
	result, err := restarted.Wait(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("resume switched to newly published bytes", err)
	}
	for _, v := range []string{first.ContentID, second.ContentID} {
		v := v
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", v), 0600) })
	}
}

func TestSelectedChineseFolderAndEmptyFileRoundTrip(t *testing.T) {
	ssh, _, _ := startSFTP(t, 0)
	root := filepath.Dir(ssh.PrivateKeyPath)
	store := filepath.Join(root, "content")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "资料", "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"中文.txt": []byte("路径下载"), "空文件.txt": nil} {
		if err := os.WriteFile(filepath.Join(root, "资料", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) { cfg.ContentDirectory = store; cfg.Shares[0].Path = root })
	node := nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}
	for _, relative := range []string{"资料", "资料/空文件.txt"} {
		version, err := publication.Publish(t.Context(), root, store, "files", relative, manifest.MinChunkSize)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", version.ContentID), 0600) })
		selected := app.ResolvePathVersion(t.Context(), node, "files", relative)
		if selected.Error != nil || selected.Version == nil {
			t.Fatal(selected)
		}
		destination := filepath.Join(t.TempDir(), selected.Version.Name)
		result, err := app.Download(t.Context(), app.DownloadRequest{Agent: node, SSH: ssh, ShareID: "files", ManifestID: selected.Version.ManifestID, Destination: destination, Workers: 2, Database: filepath.Join(t.TempDir(), "state.db")})
		if err != nil {
			t.Fatal(err)
		}
		if relative == "资料" {
			data, err := os.ReadFile(filepath.Join(result.Path, "中文.txt"))
			if err != nil || string(data) != "路径下载" {
				t.Fatal(string(data), err)
			}
			info, err := os.Stat(filepath.Join(result.Path, "空目录"))
			if err != nil || !info.IsDir() {
				t.Fatal(err)
			}
		} else {
			info, err := os.Stat(result.Path)
			if err != nil || info.Size() != 0 {
				t.Fatal(err)
			}
		}
	}
}
