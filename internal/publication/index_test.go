package publication

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

func TestFolderEmptyFileCancellationAndRootBinding(t *testing.T) {
	base := t.TempDir()
	sharedPath, store := filepath.Join(base, "share"), filepath.Join(base, "store")
	if err := os.MkdirAll(filepath.Join(sharedPath, "资料", "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedPath, "资料", "空文件.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	version, err := Publish(ctx, sharedPath, store, "files", "资料", manifest.MinChunkSize)
	if err != nil || version.PayloadKind != "folder" || version.SizeBytes == 0 {
		t.Fatal(version, err)
	}
	root, err := os.OpenRoot(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	content, err := os.OpenRoot(store)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	if got, err := Load(content, root, "files", "资料"); err != nil || got != version {
		t.Fatal(got, err)
	}
	empty, err := Publish(ctx, sharedPath, store, "files", "资料/空文件.txt", manifest.MinChunkSize)
	if err != nil || empty.SizeBytes != 0 || empty.PayloadKind != "file" {
		t.Fatal(empty, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Publish(canceled, sharedPath, store, "files", "资料", manifest.MinChunkSize); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got, err := Load(content, root, "files", "资料"); err != nil || got != version {
		t.Fatal("cancel replaced pointer", got, err)
	}
	lock, err := filesystem.Lock(filepath.Join(store, "publication.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Publish(ctx, sharedPath, store, "files", "资料", manifest.MinChunkSize)
	lock.Close()
	if !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent publisher accepted", err)
	}
	otherPath := filepath.Join(base, "other-share")
	if err := os.Mkdir(otherPath, 0700); err != nil {
		t.Fatal(err)
	}
	other, err := os.OpenRoot(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := Load(content, other, "files", "资料"); !errors.Is(err, ErrStale) {
		t.Fatal("old pointer accepted for a different share root", err)
	}
	if _, err := Publish(ctx, sharedPath, filepath.Join(sharedPath, "资料", "content"), "files", "资料", manifest.MinChunkSize); err == nil {
		t.Fatal("recursive content store accepted")
	}
	for _, value := range []string{".", "../escape", "/absolute", `a\b`, "C:/x", "a/./b", "CON.txt", "name.", "x:stream"} {
		if _, err := Publish(ctx, sharedPath, store, "files", value, manifest.MinChunkSize); err == nil {
			t.Fatal("unsafe path accepted", value)
		}
	}
	if err := os.WriteFile(filepath.Join(store, filepath.FromSlash(recordPath("files", "资料"))), []byte(`{"schema_version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(content, root, "files", "资料"); !errors.Is(err, ErrStale) {
		t.Fatal("corrupt pointer accepted", err)
	}
	t.Cleanup(func() {
		for _, v := range []string{version.ContentID, empty.ContentID} {
			_ = os.Chmod(filepath.Join(store, "objects", v), 0600)
		}
	})
}

func TestRefuseLinkedSourceAndLinkedMetadataDirectory(t *testing.T) {
	base := t.TempDir()
	shared, store := filepath.Join(base, "share"), filepath.Join(base, "store")
	if err := os.MkdirAll(filepath.Join(shared, "source"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "source", "file.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := Publish(context.Background(), shared, store, "files", "source/file.txt", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", v.ContentID), 0600) })
	link := filepath.Join(shared, "linked")
	err = os.Symlink(filepath.Join(shared, "source"), link)
	if err != nil && runtime.GOOS == "windows" {
		_, err = exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, filepath.Join(shared, "source")).CombinedOutput()
	}
	if err != nil {
		t.Skip("link creation unavailable:", err)
	}
	if _, err := Publish(context.Background(), shared, store, "files", "linked/file.txt", manifest.MinChunkSize); err == nil {
		t.Fatal("linked source accepted")
	}
	indexDir := filepath.Join(store, "index")
	backup := filepath.Join(store, "index-backup")
	if err := os.Rename(indexDir, backup); err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(backup, indexDir)
	if err != nil && runtime.GOOS == "windows" {
		_, err = exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", indexDir, backup).CombinedOutput()
	}
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(shared)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	content, err := os.OpenRoot(store)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	if _, err := Load(content, root, "files", "source/file.txt"); !errors.Is(err, ErrStale) {
		t.Fatal("linked index accepted", err)
	}
	if _, err := Publish(context.Background(), shared, store, "files", "source/file.txt", manifest.MinChunkSize); err == nil {
		t.Fatal("linked index updated")
	}
}
