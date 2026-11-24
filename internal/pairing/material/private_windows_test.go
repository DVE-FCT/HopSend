//go:build windows

package material

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func assertPrivateACL(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 2 {
		t.Fatalf("unexpected private ACL for %s: %v", filepath.Base(path), err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || (sid != user.User.Sid.String() && sid != "S-1-5-18") {
			t.Fatalf("unexpected principal in private ACL: %s", sid)
		}
	}
}

func TestWindowsPrivateMaterialsAndDirectoryLinks(t *testing.T) {
	f := fixture(t)
	dir := filepath.Join(f.store, f.expected.RequestID)
	for _, name := range []string{"", "id_ed25519", "request.json"} {
		assertPrivateACL(t, filepath.Join(dir, name))
	}
	result, err := ImportReply(t.Context(), f.store, f.data(t), f.expected)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "client.token", "connection.json"} {
		assertPrivateACL(t, filepath.Join(filepath.Dir(result.ConnectionFile), name))
	}
	linked := filepath.Join(t.TempDir(), "linked-trust")
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", linked, f.store).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture failed: %v %s", err, output)
	}
	_, err = ImportReply(t.Context(), linked, f.data(t), f.expected)
	assertCode(t, err, "invalid_store")
	paired := filepath.Join(dir, "paired")
	if info, err := os.Stat(paired); err != nil || !info.IsDir() {
		t.Fatal("original trust changed", err)
	}
}

func TestWindowsPrivateReplyCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-cache")
	for range 2 {
		if err := EnsurePrivateDirectory(path); err != nil {
			t.Fatal(err)
		}
		assertPrivateACL(t, path)
	}
	file := filepath.Join(path, "result.json")
	if err := os.WriteFile(file, []byte(`{"synthetic_credential":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	assertPrivateACL(t, file)
}

func TestWindowsExportedPairPackageACL(t *testing.T) {
	f := fixture(t)
	file := filepath.Join(t.TempDir(), "private.hopsend-pair")
	if _, err := ExportPackage(t.Context(), f.data(t), f.expected, file); err != nil {
		t.Fatal(err)
	}
	assertPrivateACL(t, file)
}
