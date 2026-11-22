package filesystem

import (
	"errors"
	"strings"
)

// SafeLeaf uses Windows rules on every platform so manifests are portable.
func SafeLeaf(name string) error {
	if name == "" || name == "." || name == ".." || strings.TrimRight(name, ". ") != name || strings.ContainsAny(name, `<>:"/\|?*`) {
		return errors.New("文件名不是安全的 Windows 单级文件名")
	}
	for _, r := range name {
		if r < 32 {
			return errors.New("文件名包含控制字符")
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return errors.New("文件名为 Windows 保留设备名")
	}
	return nil
}
