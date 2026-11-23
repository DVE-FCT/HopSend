package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/folder"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

func Publish(ctx context.Context, source, store, share string, chunkSize int64) (v1.Manifest, error) {
	if err := filesystem.CheckComponents(source); err != nil {
		return v1.Manifest{}, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return v1.Manifest{}, err
	}
	if filesystem.IsReparse(info) {
		return v1.Manifest{}, errors.New("源路径为链接或重解析点，拒绝跟随")
	}
	if !info.IsDir() {
		return manifest.Publish(ctx, source, store, share, chunkSize)
	}
	if err = filesystem.SafeLeaf(filepath.Base(source)); err != nil {
		return v1.Manifest{}, err
	}
	if err = os.MkdirAll(store, 0700); err != nil {
		return v1.Manifest{}, err
	}
	directory, err := os.MkdirTemp(store, ".packing-*")
	if err != nil {
		return v1.Manifest{}, err
	}
	defer os.Remove(directory)
	packagePath := filepath.Join(directory, filepath.Base(source))
	defer os.Remove(packagePath)
	if _, err = folder.Create(ctx, source, packagePath); err != nil {
		return v1.Manifest{}, err
	}
	return manifest.Publish(ctx, packagePath, store, share, chunkSize, "folder")
}
