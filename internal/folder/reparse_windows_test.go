//go:build windows

package folder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/filesystem"
)

func TestRealWindowsJunctionCannotBeBundledOrUsedAsOutputParent(t *testing.T) {
	source := filepath.Join(t.TempDir(), "folder")
	outside := t.TempDir()
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(source, "junction")
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture unavailable: %v %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	if _, err := Create(context.Background(), source, filepath.Join(t.TempDir(), "bundle.tar")); err == nil {
		t.Fatal("junction contents bundled")
	}
	if err := filesystem.CheckComponents(filepath.Join(junction, "out.bin")); err == nil {
		t.Fatal("junction output parent accepted")
	}
}
