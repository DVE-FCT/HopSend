//go:build !windows

package material

import "os"

func privateDirectory(path string) error { return os.Mkdir(path, 0700) }
func protectDirectory(path string) error { return os.Chmod(path, 0700) }
