//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/remoteagent"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Device struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	AgentURL          string `json:"agent_url"`
	SSHAddress        string `json:"ssh_address"`
	SSHUser           string `json:"ssh_user"`
	CAFile            string `json:"ca_file"`
	TokenFile         string `json:"token_file"`
	KeyFile           string `json:"key_file"`
	KnownHostsFile    string `json:"known_hosts_file"`
	ShareID           string `json:"share_id"`
	RemoteAgentConfig string `json:"remote_agent_config,omitempty"`
	ManageAgent       bool   `json:"manage_agent"`
}

func (d Device) request() app.DownloadRequest {
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: d.AgentURL, CACertificate: d.CAFile, TokenFile: d.TokenFile}, SSH: transport.Config{Address: d.SSHAddress, User: d.SSHUser, PrivateKeyPath: d.KeyFile, KnownHostsPath: d.KnownHostsFile}, ShareID: d.ShareID, Workers: 2, ReadTimeout: 2 * time.Minute, Retries: 6}
	if d.ManageAgent {
		r.ManagedAgentConfig = d.RemoteAgentConfig
		r.ManagedAgentBinary = app.AgentBinary()
	}
	return r
}

type TaskView struct {
	Direction   string `json:"direction"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	ManifestID  string `json:"manifest_id"`
	app.TaskStatus
}

type App struct {
	ctx                 context.Context
	manager             *app.Manager
	directory, database string
	mu                  sync.Mutex
	devices             []Device
	store               *sqlite.Store
	pending             map[string]TaskView
	startupError        string
	diagnosticCancel    context.CancelFunc
	packageReviews      map[string]packageReview
	packageImporting    bool
	pairReadLocal       func(context.Context) ([]byte, error)
	pairLocalAddresses  func() ([]string, error)
}

func NewApp() *App { return &App{} }
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	a.directory = filepath.Join(base, "HopSend", "desktop")
	if override := os.Getenv("HOPSEND_DESKTOP_DATA"); override != "" {
		a.directory = override
	}
	a.database = filepath.Join(a.directory, "state.db")
	a.pending = make(map[string]TaskView)
	if err := os.MkdirAll(a.directory, 0700); err != nil {
		a.startupError = err.Error()
		return
	}
	b, err := os.ReadFile(filepath.Join(a.directory, "devices.json"))
	if err == nil {
		if err = json.Unmarshal(b, &a.devices); err != nil {
			a.startupError = "设备配置格式无效"
			return
		}
	} else if !os.IsNotExist(err) {
		a.startupError = err.Error()
		return
	}
	a.manager = app.NewManager(a.database, func(s app.TaskStatus) { runtime.EventsEmit(ctx, "task:progress", s) })
	a.store, err = sqlite.Open(a.database)
	if err != nil {
		a.startupError = err.Error()
	}
}
func (a *App) beforeClose(context.Context) bool {
	if a.manager == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.manager.Close(ctx); err != nil {
		runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.ErrorDialog, Title: "等待任务停止", Message: "任务还在释放文件，请稍后再关闭。"})
		return true
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	remoteagent.Default.Close()
	return false
}
func (a *App) Ready() error {
	if a.startupError != "" {
		return errors.New(a.startupError)
	}
	if a.manager == nil {
		return errors.New("任务内核尚未就绪")
	}
	return nil
}
func (a *App) Devices() ([]Device, error) {
	if err := a.Ready(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Device{}, a.devices...), nil
}
func (a *App) SaveDevice(d Device) (Device, error) {
	if err := a.Ready(); err != nil {
		return d, err
	}
	if d.Name == "" || d.AgentURL == "" || d.SSHAddress == "" || d.SSHUser == "" || d.CAFile == "" || d.TokenFile == "" || d.KeyFile == "" || d.KnownHostsFile == "" || d.ShareID == "" {
		return d, errors.New("请完整填写设备连接与信任文件")
	}
	if d.ManageAgent && d.RemoteAgentConfig == "" {
		return d, errors.New("启用自动准备时，需要填写首次配对生成的远端 Agent 配置路径")
	}
	if d.ID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return d, err
		}
		d.ID = hex.EncodeToString(b)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	devices := append([]Device{}, a.devices...)
	found := false
	for i := range devices {
		if devices[i].ID == d.ID {
			devices[i] = d
			found = true
			break
		}
	}
	if !found {
		devices = append(devices, d)
	}
	b, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return d, err
	}
	path := filepath.Join(a.directory, "devices.json")
	if err = writeDeviceSnapshot(path, b); err != nil {
		return d, err
	}
	a.devices = devices
	return d, nil
}
func (a *App) findDevice(id string) (Device, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if d.ID == id {
			return d, nil
		}
	}
	return Device{}, errors.New("请选择已保存设备")
}
func (a *App) PickFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
}

func (a *App) PickFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要上传的文件夹"})
}
func (a *App) PickDestination(name string) (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "选择下载保存位置", DefaultFilename: name})
}
func (a *App) ImportDevice() (Device, error) {
	p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "导入配对配置（只含文件引用）", Filters: []runtime.FileFilter{{DisplayName: "JSON 配置", Pattern: "*.json"}}})
	if err != nil || p == "" {
		return Device{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Device{}, err
	}
	if len(b) > 1<<20 {
		return Device{}, errors.New("配对配置超过 1 MiB")
	}
	var d Device
	if err = json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	d.ID = ""
	return d, nil
}
func (a *App) CheckDevice(id string) (map[string]string, error) {
	d, err := a.findDevice(id)
	if err != nil {
		return nil, err
	}
	r := d.request()
	if d.ManageAgent {
		_, err := a.PrepareDevice(id)
		if err != nil {
			return nil, err
		}
		return map[string]string{"message": "设备准备、HTTPS/SSH 认证及真实 SFTP 字节测试全部通过", "observed_at": time.Now().Format(time.RFC3339)}, nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if _, err = c.Health(ctx); err != nil {
		return nil, err
	}
	if _, err = c.Shares(ctx); err != nil {
		return nil, err
	}
	s, err := transport.Dial(ctx, r.SSH)
	if err != nil {
		return nil, err
	}
	_ = s.Close()
	return map[string]string{"message": "HTTPS 证书、API 凭据、SSH 主机密钥与公钥认证均通过", "observed_at": time.Now().Format(time.RFC3339)}, nil
}
func (a *App) StartDownload(deviceID, manifestID, destination string) (string, error) {
	if err := a.Ready(); err != nil {
		return "", err
	}
	d, err := a.findDevice(deviceID)
	if err != nil {
		return "", err
	}
	r := d.request()
	if d.ManageAgent {
		if _, err = a.PrepareDevice(deviceID); err != nil {
			return "", err
		}
	}
	r.ManifestID, r.Destination = manifestID, destination
	id, err := a.manager.Start(a.ctx, r)
	if err == nil {
		a.mu.Lock()
		a.pending[id] = TaskView{ID: id, Name: filepath.Base(destination), Destination: destination, ManifestID: manifestID}
		a.mu.Unlock()
	}
	return id, err
}

func (a *App) StartUpload(deviceID, shareID, source, target string) (string, error) {
	if err := a.Ready(); err != nil {
		return "", err
	}
	d, err := a.findDevice(deviceID)
	if err != nil {
		return "", err
	}
	r := d.request()
	if d.ManageAgent {
		if _, err = a.PrepareDevice(deviceID); err != nil {
			return "", err
		}
	}
	r.Database, r.ShareID = a.database, shareID
	r, err = app.PrepareUpload(a.ctx, r, source, target, 32<<20)
	if err != nil {
		return "", err
	}
	id, err := a.manager.Start(a.ctx, r)
	if err == nil {
		a.mu.Lock()
		a.pending[id] = TaskView{ID: id, Direction: "upload", Name: filepath.Base(source), Destination: r.Destination, ManifestID: r.ManifestID}
		a.mu.Unlock()
	}
	return id, err
}
func (a *App) PauseTask(id string) error            { return a.manager.Pause(a.ctx, id) }
func (a *App) ResumeTask(id string) (string, error) { return a.manager.Resume(a.ctx, id) }
func (a *App) CancelTask(id string) error           { return a.manager.Cancel(a.ctx, id) }
func (a *App) Tasks() ([]TaskView, error) {
	if err := a.Ready(); err != nil {
		return nil, err
	}
	tasks, err := a.store.List(a.ctx)
	if err != nil {
		return nil, err
	}
	views := make([]TaskView, 0, len(tasks))
	known := make(map[string]bool)
	for _, t := range tasks {
		known[t.ID] = true
		status := app.TaskStatus{Progress: engine.Progress{TaskID: t.ID, State: t.State, TotalBytes: t.Manifest.SizeBytes, CompletedChunks: len(t.Confirmed)}, UpdatedAt: t.UpdatedAt}
		for _, i := range t.Confirmed {
			status.VerifiedBytes += t.Manifest.Chunks[i].Length
		}
		if live, ok := a.manager.Status(t.ID); ok {
			status = live
		} else if status.State != "completed" && status.State != "cancelled" && status.State != "paused" && status.State != "failed" {
			status.State = "recoverable"
		}
		var request app.DownloadRequest
		_ = json.Unmarshal(t.Source, &request)
		direction := request.Direction
		if direction == "" {
			direction = "download"
		}
		views = append(views, TaskView{ID: t.ID, Direction: direction, Name: t.Manifest.Name, Destination: t.Destination, ManifestID: t.Manifest.ManifestID, TaskStatus: status})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, pending := range a.pending {
		if !known[id] {
			if status, ok := a.manager.Status(id); ok {
				pending.TaskStatus = status
				views = append(views, pending)
			}
		}
	}
	return views, nil
}

func (a *App) DiagnoseDevice(id, manifestID string) (app.NetworkReport, error) {
	if err := a.Ready(); err != nil {
		return app.NetworkReport{}, err
	}
	d, err := a.findDevice(id)
	if err != nil {
		return app.NetworkReport{}, err
	}
	a.mu.Lock()
	if a.diagnosticCancel != nil {
		a.mu.Unlock()
		return app.NetworkReport{}, errors.New("已有诊断正在运行")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 11*time.Minute)
	a.diagnosticCancel = cancel
	a.mu.Unlock()
	defer func() { cancel(); a.mu.Lock(); a.diagnosticCancel = nil; a.mu.Unlock() }()
	r := d.request()
	if d.ManageAgent {
		if _, err = a.prepareDevice(ctx, id); err != nil {
			return app.NetworkReport{}, err
		}
	}
	r.ManifestID = manifestID
	return app.Diagnose(ctx, r), nil
}
func (a *App) StopDiagnostic() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.diagnosticCancel != nil {
		a.diagnosticCancel()
	}
}
func (a *App) Activity() ([]app.TaskStatus, error) {
	if err := a.Ready(); err != nil {
		return nil, err
	}
	return a.manager.Logs()
}

func (a *App) PrepareDevice(id string) (remoteagent.Report, error) {
	return a.prepareDevice(a.ctx, id)
}
func (a *App) prepareDevice(parent context.Context, id string) (remoteagent.Report, error) {
	if err := a.Ready(); err != nil {
		return remoteagent.Report{}, err
	}
	d, err := a.findDevice(id)
	if err != nil {
		return remoteagent.Report{}, err
	}
	if !d.ManageAgent {
		return remoteagent.Report{}, errors.New("请先在设备设置启用自动准备，并填写已配对的远端 Agent 配置路径")
	}
	r := d.request()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	return remoteagent.Default.Prepare(ctx, remoteagent.Request{SSH: r.SSH, Agent: r.Agent, ConfigPath: d.RemoteAgentConfig, Binary: app.AgentBinary(), ShareID: d.ShareID})
}
