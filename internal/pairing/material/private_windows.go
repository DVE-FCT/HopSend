//go:build windows

package material

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supply a protected DACL at creation time; mode 0700 alone has no equivalent
// protection on Windows. Children inherit only the current user and SYSTEM.
func privateDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &sa); err != nil {
		return &os.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return nil
}

// Legacy request directories may predate the Go implementation. Verify their
// owner before restricting the DACL; never take ownership of somebody's files.
func protectDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	existing, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := existing.Owner()
	if err != nil || !windows.EqualSid(owner, user.User.Sid) {
		return errors.New("配对目录属于其他账号")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
