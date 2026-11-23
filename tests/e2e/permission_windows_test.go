//go:build windows

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"golang.org/x/sys/windows"
)

func TestRealWindowsReadOnlyACLPreventsDownloadWithoutPublishing(t *testing.T) {
	root := recoveryFixture(t)
	opts, db := recoveryOptions(t, root)
	defer db.Close()
	directory := filepath.Join(root, "denied")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := "*" + user.User.Sid.String()
	defer func() {
		if output, err := exec.Command("icacls.exe", directory, "/inheritance:e", "/grant:r", sid+":(OI)(CI)(F)").CombinedOutput(); err != nil {
			t.Errorf("restore task fixture ACL: %v %s", err, output)
		}
	}()
	if output, err := exec.Command("icacls.exe", directory, "/inheritance:r", "/grant:r", sid+":(OI)(CI)(RX)").CombinedOutput(); err != nil {
		t.Fatalf("test ACL setup: %v %s", err, output)
	}
	opts.Destination = filepath.Join(directory, "result.bin")
	if _, err = engine.Download(context.Background(), opts); err == nil {
		t.Fatal("ACL-denied directory accepted")
	}
	if _, err = os.Stat(opts.Destination); !os.IsNotExist(err) {
		t.Fatal("unwritable target published")
	}
}
