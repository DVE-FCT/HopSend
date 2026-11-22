//go:build !windows

package filesystem

import "os"

// The non-Windows adapter publishes by an atomic no-replace hard link, then
// removes staging. It requires one filesystem; Windows uses native rename.
func PublishNoReplace(staging, destination string) error {
	if err := os.Link(staging, destination); err != nil {
		return err
	}
	return os.Remove(staging)
}
