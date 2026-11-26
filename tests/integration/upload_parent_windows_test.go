package integration

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestUploadParentNTFSDenialPreventsSessionAllocation(t *testing.T) {
	r, _, inbox := uploadFixture(t)
	parent := filepath.Join(inbox, "denied")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(os.Getenv("WINDIR"), "System32", "icacls.exe")
	if output, err := exec.Command(tool, parent, "/deny", "*"+current.Uid+":(RD)").CombinedOutput(); err != nil {
		t.Fatalf("cannot prepare isolated NTFS denial: %v %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(tool, parent, "/remove:d", "*"+current.Uid).CombinedOutput(); err != nil {
			t.Errorf("restore isolated directory ACL: %v %s", err, output)
		}
	})
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(root, r.ManifestID)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.CreateUpload(t.Context(), v1.CreateUpload{RequestID: strings.Repeat("a", 64), ShareID: r.ShareID, ParentPath: "denied", Target: "blocked.bin", Manifest: m}); err == nil {
		t.Fatal("NTFS-denied parent accepted")
	}
	if entries, err := os.ReadDir(filepath.Join(inbox, ".hopsend-uploads")); err == nil && len(entries) > 0 {
		t.Fatal("unusable parent allocated a staging session")
	}
}
