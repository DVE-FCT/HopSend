package httpapi

import (
	"errors"
	"os"
	"path"
	"path/filepath"

	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

func uploadTarget(record sqlite.UploadRecord) string {
	return path.Join(record.ParentPath, record.Target)
}

// The lease spans each entire control request, including final filesystem
// publication. Staging is outside the user-selected parent, so SFTP byte writes
// cannot be redirected when the parent changes between requests.
func leaseUploadParent(root *os.Root, parent, expected string) (string, func(), error) {
	noop := func() {}
	clean, err := filesystem.UploadParent(parent)
	if err != nil || clean != parent {
		return "", noop, errors.New("上传父目录格式无效")
	}
	relative := parent
	if relative == "" {
		relative = "."
	}
	physical := filepath.Join(root.Name(), filepath.FromSlash(relative))
	pinned, release, err := filesystem.LeaseDirectory(physical)
	if err != nil {
		return "", noop, errors.New("上传父目录不可用、被占用或经过链接")
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	base, err := root.Open(".")
	if err != nil {
		return "", noop, err
	}
	defer base.Close()
	baseInfo, err := base.Stat()
	if err != nil {
		return "", noop, err
	}
	actualBase, err := os.Lstat(root.Name())
	if err != nil || filesystem.IsReparse(actualBase) || !os.SameFile(baseInfo, actualBase) {
		return "", noop, errors.New("共享根身份已变化")
	}
	f, err := root.Open(relative)
	if err != nil {
		return "", noop, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return "", noop, errors.New("上传父目录不是常规目录")
	}
	pinInfo, err := pinned.Stat()
	if err != nil || !os.SameFile(info, pinInfo) {
		return "", noop, errors.New("上传父目录在读取期间改变")
	}
	baseID, err := filesystem.Identity(base)
	if err != nil {
		return "", noop, err
	}
	parentID, err := filesystem.Identity(f)
	if err != nil {
		return "", noop, err
	}
	identity := baseID + "|" + parentID
	if expected != "" && identity != expected {
		return "", noop, errors.New("上传父目录已被替换，拒绝恢复或发布到新目录")
	}
	failed = false
	return identity, release, nil
}
