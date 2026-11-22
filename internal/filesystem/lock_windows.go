//go:build windows

package filesystem

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Lock uses a kernel-owned exclusive handle. Process death releases it; the
// marker is deliberately retained to avoid unlink/recreate ownership races.
func Lock(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("目标被其他进程使用或无法取得写入锁: %w", err)
	}
	return os.NewFile(uintptr(h), path), nil
}
