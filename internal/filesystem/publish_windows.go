package filesystem

import "golang.org/x/sys/windows"

// PublishNoReplace performs same-volume atomic rename and never overwrites a
// concurrently created destination. Callers must sync and close writers first.
func PublishNoReplace(staging, destination string) error {
	from, err := windows.UTF16PtrFromString(staging)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
