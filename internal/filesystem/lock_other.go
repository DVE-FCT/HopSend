//go:build !windows

package filesystem

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("目标被其他进程使用: %w", err)
	}
	return f, nil
}
