package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// LeaseDirectory pins the absolute directory chain without FILE_SHARE_DELETE.
// Windows then refuses rename/delete/replacement until release. Reparse points
// are opened themselves and rejected rather than followed.
func LeaseDirectory(path string) (file *os.File, release func(), returnedErr error) {
	files := []*os.File{}
	release = func() {
		for i := len(files) - 1; i >= 0; i-- {
			_ = files[i].Close()
		}
	}
	defer func() {
		if returnedErr != nil {
			release()
		}
	}()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, release, err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	parts := []string{current}
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator)) {
		if part != "" {
			current = filepath.Join(current, part)
			parts = append(parts, current)
		}
	}
	for _, component := range parts {
		nativePath := component
		if !strings.HasPrefix(nativePath, `\\?\`) {
			if strings.HasPrefix(nativePath, `\\`) {
				nativePath = `\\?\UNC\` + strings.TrimPrefix(nativePath, `\\`)
			} else {
				nativePath = `\\?\` + nativePath
			}
		}
		p, err := windows.UTF16PtrFromString(nativePath)
		if err != nil {
			return nil, release, err
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return nil, release, errors.New("目标目录无法取得稳定句柄，请确认权限或稍后重试")
		}
		opened := os.NewFile(uintptr(h), component)
		files = append(files, opened)
		info, err := opened.Stat()
		if err != nil || !info.IsDir() || IsReparse(info) {
			return nil, release, errors.New("目标路径不是常规目录或经过重解析点")
		}
		file = opened
	}
	return file, release, nil
}
