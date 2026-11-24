//go:build windows

package main

import (
	"os"
	"path/filepath"

	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"golang.org/x/sys/windows"
)

func writeDeviceSnapshot(path string, data []byte) error {
	if err := filesystem.CheckComponents(path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".devices-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { temporary.Close(); os.Remove(name) }()
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	source, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
