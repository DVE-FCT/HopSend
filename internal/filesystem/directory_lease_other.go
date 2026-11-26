//go:build !windows

package filesystem

import "os"

// HopSend's production upload directory lease is Windows-specific. os.Root
// continues to provide traversal confinement on development hosts.
func LeaseDirectory(path string) (*os.File, func(), error) {
	if err := CheckComponents(path); err != nil {
		return nil, func() {}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}
