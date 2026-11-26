package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryLeasePreventsRenameUntilRelease(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	f, release, err := LeaseDirectory(parent)
	if err != nil {
		t.Fatal(err)
	}
	id, err := Identity(f)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-moved"); err == nil {
		release()
		t.Fatal("leased directory renamed during publication")
	}
	release()
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	other, closeOther, err := LeaseDirectory(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOther()
	newID, err := Identity(other)
	if err != nil || newID == id {
		t.Fatal("replacement directory retained identity", newID, err)
	}
}

func TestDirectoryLeaseSupportsLongChineseParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), strings.Repeat("中文目录", 20), strings.Repeat("第二级", 20), strings.Repeat("第三级", 20))
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	f, release, err := LeaseDirectory(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Identity(f); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-moved"); err == nil {
		t.Fatal("long path lease did not block rename")
	}
}
