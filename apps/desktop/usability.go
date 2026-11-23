//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/buildinfo"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"golang.org/x/sys/windows"
)

type DeviceObservation struct {
	Reachable    bool   `json:"reachable"`
	ObservedAt   string `json:"observed_at"`
	Detail       string `json:"detail"`
	AgentVersion string `json:"agent_version,omitempty"`
}

// ObserveDevice is deliberately read-only: viewing device cards never deploys
// software or starts an Agent. It describes HTTPS reachability, not file access.
func (a *App) ObserveDevice(id string) (DeviceObservation, error) {
	if err := a.Ready(); err != nil {
		return DeviceObservation{}, err
	}
	d, err := a.findDevice(id)
	if err != nil {
		return DeviceObservation{}, err
	}
	observation := DeviceObservation{}
	c, err := nodeclient.New(d.request().Agent)
	if err == nil {
		defer c.Close()
		ctx, cancel := context.WithTimeout(a.ctx, 6*time.Second)
		defer cancel()
		value, e := c.Health(ctx)
		err = e
		if err == nil {
			observation.Reachable = true
			observation.AgentVersion = value.Version
			observation.Detail = "HTTPS 证书与 API 健康检查通过；文件访问需独立验收"
		}
	}
	if err != nil {
		observation.Detail = err.Error()
	}
	observation.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return observation, nil
}

func (a *App) ApplicationInfo() map[string]string {
	return map[string]string{"version": buildinfo.Version}
}

func localTaskTarget(task engine.Task) (string, error) {
	if task.State != "completed" {
		return "", errors.New("只有已完成的本机下载可以在资源管理器中显示")
	}
	var request app.DownloadRequest
	if err := json.Unmarshal(task.Source, &request); err != nil {
		return "", errors.New("任务来源记录无效")
	}
	if request.Direction == "upload" {
		return "", errors.New("上传目标在远端设备，请到接收设备查看")
	}
	if !filepath.IsAbs(task.Destination) {
		return "", errors.New("任务目标不是本机绝对路径")
	}
	if err := filesystem.CheckComponents(task.Destination); err != nil {
		return "", err
	}
	info, err := os.Stat(task.Destination)
	if err != nil {
		return "", errors.New("下载目标已移动或不可访问")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", errors.New("下载目标不是常规文件或目录")
	}
	return filepath.Clean(task.Destination), nil
}

func (a *App) RevealTask(id string) error {
	if err := a.Ready(); err != nil {
		return err
	}
	task, err := a.store.Get(a.ctx, id)
	if err != nil {
		return errors.New("未找到持久保存的下载任务")
	}
	target, err := localTaskTarget(task)
	if err != nil {
		return err
	}
	return revealInExplorer(target)
}

// Pass a parsed item ID to the Windows shell. No shell command or downloaded
// file is executed, including for names containing quotes or punctuation.
func revealInExplorer(target string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := windows.NewLazySystemDLL("ole32.dll")
	init := ole.NewProc("CoInitializeEx")
	uninit := ole.NewProc("CoUninitialize")
	free := ole.NewProc("CoTaskMemFree")
	status, _, _ := init.Call(0, 2)
	if int32(status) < 0 {
		return errors.New("无法初始化 Windows 资源管理器接口")
	}
	defer uninit.Call()
	shell := windows.NewLazySystemDLL("shell32.dll")
	name, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	var pidl uintptr
	status, _, _ = shell.NewProc("SHParseDisplayName").Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0)
	if int32(status) < 0 || pidl == 0 {
		return errors.New("Windows 无法定位此下载目标")
	}
	defer free.Call(pidl)
	status, _, _ = shell.NewProc("SHOpenFolderAndSelectItems").Call(pidl, 0, 0, 0)
	if int32(status) < 0 {
		return errors.New("无法在资源管理器中显示下载目标")
	}
	return nil
}
