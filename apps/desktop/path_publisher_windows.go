package main

import (
	"errors"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/publication"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"path/filepath"
)

func (a *App) sourcePublisher() *app.PathPublisher {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.publisher == nil {
		a.publisher = &app.PathPublisher{}
	}
	return a.publisher
}

func (a *App) PickPublicationSource(config, revision, share string, folder bool) (string, error) {
	if err := a.Ready(); err != nil {
		return "", err
	}
	settings, err := app.ReadPublicationConfig(config)
	if err != nil {
		return "", err
	}
	if settings.Revision != revision {
		return "", errors.New("配置已变化，请重新读取共享")
	}
	root := ""
	for _, s := range settings.Shares {
		if s.ID == share {
			root = s.Path
		}
	}
	if root == "" {
		return "", errors.New("请选择有效共享")
	}
	opts := runtime.OpenDialogOptions{Title: "选择共享内要发布的内容", DefaultDirectory: root}
	var selected string
	if folder {
		selected, err = runtime.OpenDirectoryDialog(a.ctx, opts)
	} else {
		selected, err = runtime.OpenFileDialog(a.ctx, opts)
	}
	if err != nil || selected == "" {
		return "", err
	}
	relative, err := filepath.Rel(root, selected)
	if err != nil {
		return "", errors.New("选择内容必须位于此共享目录内")
	}
	relative = filepath.ToSlash(relative)
	if publication.ValidatePath(relative) != nil {
		return "", errors.New("请选择共享内部的文件或子目录，不能选择共享根或其他磁盘")
	}
	return relative, nil
}
func (a *App) ReadPublicationConfig(path string) (app.PublicationConfig, error) {
	if err := a.Ready(); err != nil {
		return app.PublicationConfig{}, err
	}
	return app.ReadPublicationConfig(path)
}
func (a *App) StartPathPublication(config, revision, share, path string) (app.PublicationJob, error) {
	if err := a.Ready(); err != nil {
		return app.PublicationJob{}, err
	}
	return a.sourcePublisher().Start(a.ctx, config, revision, share, path)
}
func (a *App) PathPublicationStatus() app.PublicationJob { return a.sourcePublisher().Status() }
func (a *App) CancelPathPublication(id string) error     { return a.sourcePublisher().Cancel(id) }
