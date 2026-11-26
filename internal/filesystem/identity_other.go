//go:build !windows

package filesystem

import (
	"fmt"
	"os"
	"syscall"
)

func Identity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("filesystem identity unavailable")
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}

func Replace(staging, destination string) error { return os.Rename(staging, destination) }
