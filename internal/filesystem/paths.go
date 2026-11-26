package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// UploadParent canonicalizes the shared-root default while applying portable
// Windows rules to every non-root directory component.
func UploadParent(value string) (string, error) {
	if value == "" || value == "." {
		return "", nil
	}
	if len(value) > 4096 || !fs.ValidPath(value) || strings.Contains(value, `\`) {
		return "", errors.New("上传父目录必须为共享内规范相对路径")
	}
	for _, part := range strings.Split(value, "/") {
		if err := SafeLeaf(part); err != nil {
			return "", err
		}
		if strings.EqualFold(part, ".hopsend-uploads") {
			return "", errors.New("不能将会话暂存区作为上传目录")
		}
	}
	return value, nil
}

// SafeLeaf uses Windows rules on every platform so manifests are portable.
func SafeLeaf(name string) error {
	if len(utf16.Encode([]rune(name))) > 200 {
		return errors.New("单级文件名超过当前便携长度上限")
	}
	if name == "" || name == "." || name == ".." || strings.TrimRight(name, ". ") != name || strings.ContainsAny(name, `<>:"/\|?*`) {
		return errors.New("文件名不是安全的 Windows 单级文件名")
	}
	for _, r := range name {
		if r < 32 {
			return errors.New("文件名包含控制字符")
		}
	}
	base := strings.TrimRight(strings.ToUpper(strings.SplitN(name, ".", 2)[0]), " .")
	base = strings.NewReplacer("¹", "1", "²", "2", "³", "3").Replace(base)
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return errors.New("文件名为 Windows 保留设备名")
	}
	return nil
}

// CheckComponents rejects existing link/reparse components before task writes.
func CheckComponents(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if IsReparse(info) {
			return errors.New("路径经过链接或重解析点，拒绝跟随")
		}
	}
	return nil
}
