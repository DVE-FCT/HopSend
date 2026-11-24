//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/pairing/material"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type PairRequestResult struct {
	Request     v1.Request `json:"request"`
	RequestFile string     `json:"request_file"`
	RequestJSON string     `json:"request_json"`
}
type PairInspectResult struct {
	Device            v1.ReplyDevice `json:"device"`
	MaterialsVerified bool           `json:"materials_verified"`
	TrustVerified     bool           `json:"trust_verified"`
	PackageSHA256     string         `json:"package_sha256"`
}
type PairIdentity struct {
	SourceIP string `json:"source_ip"`
	Name     string `json:"name"`
}
type packageReview struct {
	path    string
	preview material.PackagePreview
	expires time.Time
}

func pairingTrustRoot() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" || !filepath.IsAbs(base) {
		return "", errors.New("当前账号缺少有效的本地应用数据目录")
	}
	return filepath.Join(base, "HopSend", "trust"), nil
}

func parsePairIdentity(data []byte) (PairIdentity, error) {
	var state struct {
		BackendState string
		Self         struct{ TailscaleIPs []string }
	}
	if len(data) > 4<<20 || json.Unmarshal(data, &state) != nil || state.BackendState != "Running" {
		return PairIdentity{}, errors.New("请先安装、登录并连接 Tailscale")
	}
	address := ""
	prefix := netip.MustParsePrefix("100.64.0.0/10")
	for _, value := range state.Self.TailscaleIPs {
		ip, err := netip.ParseAddr(value)
		if err == nil && ip.Is4() && ip.String() == value && prefix.Contains(ip) {
			if address != "" {
				return PairIdentity{}, errors.New("本机 Tailscale IPv4 不唯一")
			}
			address = value
		}
	}
	if address == "" {
		return PairIdentity{}, errors.New("本机尚无可验证的 Tailscale IPv4")
	}
	name, _ := os.Hostname()
	return PairIdentity{SourceIP: address, Name: name}, nil
}

func (a *App) PairLocalIdentity() (PairIdentity, error) {
	if err := a.Ready(); err != nil {
		return PairIdentity{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	var data []byte
	var err error
	if a.pairReadLocal != nil {
		data, err = a.pairReadLocal(ctx)
	} else {
		command := exec.CommandContext(ctx, filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe"), "status", "--json")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		data, err = command.Output()
	}
	if err != nil {
		return PairIdentity{}, errors.New("无法读取本机 Tailscale，请先登录并连接")
	}
	return parsePairIdentity(data)
}

// PairCreateRequest keeps the SSH private key local and returns public data only.
func (a *App) PairCreateRequest(sourceIP, name string) (PairRequestResult, error) {
	identity, err := a.PairLocalIdentity()
	if err != nil {
		return PairRequestResult{}, err
	}
	if sourceIP != identity.SourceIP {
		return PairRequestResult{}, errors.New("本机地址已变化，请重新读取并确认 Tailscale IP")
	}
	trust, err := pairingTrustRoot()
	if err != nil {
		return PairRequestResult{}, err
	}
	output := filepath.Join(a.directory, "pair-requests")
	if err := os.MkdirAll(output, 0700); err != nil {
		return PairRequestResult{}, err
	}
	result, err := material.CreateRequest(a.ctx, material.RequestOptions{TrustDirectory: trust, OutputDirectory: output, SourceIP: sourceIP, Name: name})
	if err != nil {
		return PairRequestResult{}, err
	}
	data, err := json.MarshalIndent(result.Request, "", "  ")
	if err != nil {
		return PairRequestResult{}, err
	}
	return PairRequestResult{Request: result.Request, RequestFile: result.RequestFile, RequestJSON: string(data)}, nil
}

func (a *App) PickPairPackage() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择回应或配对包", Filters: []runtime.FileFilter{{DisplayName: "HopSend 回应/配对包", Pattern: "*.zip;*.hopsend-pair"}}})
}

func (a *App) CreatePairRequest(existingDevice string) (material.RequestResult, error) {
	identity, err := a.PairLocalIdentity()
	if err != nil {
		return material.RequestResult{}, err
	}
	device, err := a.findDevice(existingDevice)
	if err != nil {
		return material.RequestResult{}, err
	}
	address, _, err := net.SplitHostPort(device.SSHAddress)
	if err != nil {
		return material.RequestResult{}, err
	}
	trust, err := pairingTrustRoot()
	if err != nil {
		return material.RequestResult{}, err
	}
	original, err := material.LocalRequest(trust, device.ID, address)
	if err != nil {
		return material.RequestResult{}, err
	}
	if original.SourceIP != identity.SourceIP {
		return material.RequestResult{}, errors.New("本机组网地址已变化，请创建新请求")
	}
	output, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "保存原公开请求"})
	if err != nil || output == "" {
		return material.RequestResult{}, err
	}
	return material.ExportRequest(trust, device.ID, address, filepath.Join(output, "HopSend-pair-request-"+device.ID+".json"))
}

// PairInspectReply does not persist credentials or establish trust. The returned
// pins are display data and must be checked against the source independently.
func (a *App) PairInspectReply(replyPath string) (PairInspectResult, error) {
	if err := a.Ready(); err != nil {
		return PairInspectResult{}, err
	}
	path, err := filepath.Abs(replyPath)
	if err != nil {
		return PairInspectResult{}, err
	}
	data, err := material.ReadPackage(path)
	if err != nil {
		return PairInspectResult{}, err
	}
	preview, err := material.PreviewPackage(data)
	if err != nil {
		return PairInspectResult{}, err
	}
	trust, err := pairingTrustRoot()
	if err != nil {
		return PairInspectResult{}, err
	}
	address, _, err := net.SplitHostPort(preview.Device.SSHAddress)
	if err != nil {
		return PairInspectResult{}, err
	}
	if _, err := material.LocalRequest(trust, preview.Device.RequestID, address); err != nil {
		return PairInspectResult{}, err
	}
	a.mu.Lock()
	a.packageReviews = map[string]packageReview{path: {path: path, preview: preview, expires: time.Now().Add(15 * time.Minute)}}
	a.mu.Unlock()
	return PairInspectResult{Device: preview.Device, MaterialsVerified: true, TrustVerified: false, PackageSHA256: preview.PackageSHA256}, nil
}

// PairImportReply imports only the exact previewed package and saves an
// unverified device. PrepareDevice remains the explicit live acceptance step.
func (a *App) PairImportReply(replyPath, expectedSSH, expectedCA string) (string, error) {
	if err := a.Ready(); err != nil {
		return "", err
	}
	path, err := filepath.Abs(replyPath)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	review, ok := a.packageReviews[path]
	if !ok || time.Now().After(review.expires) || a.packageImporting {
		a.mu.Unlock()
		return "", errors.New("请先检视回应并完成指纹核对；预览已过期、已替换或正在导入")
	}
	a.packageImporting = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.packageImporting = false; a.mu.Unlock() }()
	data, err := material.ReadPackage(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != review.preview.PackageSHA256 {
		return "", errors.New("回应文件在检视后发生变化，请重新检视并核对")
	}
	trust, err := pairingTrustRoot()
	if err != nil {
		return "", err
	}
	address, _, _ := net.SplitHostPort(review.preview.Device.SSHAddress)
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	result, err := material.ImportReply(ctx, trust, data, material.Expected{SSHFingerprint: expectedSSH, CASHA256: expectedCA, RequestID: review.preview.Device.RequestID, ReceiverIP: address})
	if err != nil {
		return "", err
	}
	config, err := os.ReadFile(result.ConnectionFile)
	if err != nil || len(config) > v1.MaxJSONBytes {
		return "", errors.New("导入后的连接配置不可读取")
	}
	var device Device
	if err := json.Unmarshal(config, &device); err != nil {
		return "", err
	}
	device.ID = review.preview.Device.RequestID
	if _, err := a.SaveDevice(device); err != nil {
		return "", err
	}
	return result.ConnectionFile, nil
}
