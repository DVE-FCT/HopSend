package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

type DiagnosticLayer struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Detail       string `json:"detail"`
	Milliseconds int64  `json:"milliseconds"`
}
type NetworkReport struct {
	ObservedAt       string            `json:"observed_at"`
	Path             string            `json:"path"`
	Layers           []DiagnosticLayer `json:"layers"`
	TransferVerified bool              `json:"transfer_verified"`
	DataBytes        int64             `json:"data_bytes"`
}

func Diagnose(ctx context.Context, r DownloadRequest) NetworkReport {
	return diagnose(ctx, r, readTailscale)
}

func readTailscale(ctx context.Context) ([]byte, error) {
	command, err := exec.LookPath("tailscale")
	if err != nil {
		command = filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe")
	}
	process := exec.CommandContext(ctx, command, "status", "--json")
	hideDiagnosticWindow(process)
	return process.Output()
}

func diagnose(ctx context.Context, r DownloadRequest, status func(context.Context) ([]byte, error)) NetworkReport {
	report := NetworkReport{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Path: "unknown", Layers: []DiagnosticLayer{}}
	add := func(name, state, detail string, started time.Time) {
		report.Layers = append(report.Layers, DiagnosticLayer{Name: name, State: state, Detail: detail, Milliseconds: time.Since(started).Milliseconds()})
	}
	started := time.Now()
	host, _, err := net.SplitHostPort(r.SSH.Address)
	if err != nil {
		add("address", "failed", "SSH 地址必须为 host:port", started)
		return report
	}
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	output, tsErr := status(probe)
	cancel()
	if tsErr != nil {
		add("tailscale", "unknown", "无法读取本机 Tailscale；继续检查实际应用连接", started)
	} else {
		var value struct {
			BackendState string
			Peer         map[string]struct {
				TailscaleIPs []string
				DNSName      string
				Online       bool
				Active       bool
				CurAddr      string
				Relay        string
			}
		}
		if err = json.Unmarshal(output, &value); err != nil {
			add("tailscale", "unknown", "Tailscale 状态响应无效", started)
		} else {
			found := false
			for _, peer := range value.Peer {
				match := strings.EqualFold(strings.TrimSuffix(peer.DNSName, "."), host)
				for _, ip := range peer.TailscaleIPs {
					if ip == host {
						match = true
					}
				}
				if match {
					found = true
					state := "ok"
					detail := "设备在线；仅设备可见不代表应用可用"
					if !peer.Online {
						state = "failed"
						detail = "设备离线"
					}
					if peer.Online && peer.Active && peer.CurAddr != "" {
						report.Path = "direct"
						detail = "当前活动连接为直连"
					} else if peer.Online && peer.Active && peer.Relay != "" {
						report.Path = "relay"
						detail = "当前活动连接经 DERP 中继：" + peer.Relay
					}
					add("tailscale", state, detail, started)
					break
				}
			}
			if !found {
				add("tailscale", "unknown", "当前尾网状态未找到此地址；以实际端口和认证检查为准", started)
			}
		}
	}
	u, err := url.Parse(r.Agent.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		add("agent_address", "failed", "Agent 地址需要 HTTPS", time.Now())
		return report
	}
	httpsAddress := u.Host
	if u.Port() == "" {
		httpsAddress = net.JoinHostPort(u.Hostname(), "443")
	}
	for _, item := range []struct{ name, address string }{{"https_port", httpsAddress}, {"ssh_port", r.SSH.Address}} {
		started = time.Now()
		probe, cancel = context.WithTimeout(ctx, 5*time.Second)
		conn, e := (&net.Dialer{}).DialContext(probe, "tcp", item.address)
		cancel()
		if e != nil {
			add(item.name, "failed", e.Error(), started)
		} else {
			_ = conn.Close()
			add(item.name, "ok", "TCP 可达，身份验证另行检查", started)
		}
	}
	started = time.Now()
	client, err := nodeclient.New(r.Agent)
	httpsReady := false
	if err != nil {
		add("https_identity", "failed", err.Error(), started)
	} else {
		defer client.Close()
		probe, cancel = context.WithTimeout(ctx, 10*time.Second)
		_, err = client.Health(probe)
		if err == nil {
			_, err = client.Shares(probe)
		}
		cancel()
		if err != nil {
			add("https_identity", "failed", err.Error(), started)
		} else {
			httpsReady = true
			add("https_identity", "ok", "TLS 证书、主机名和 API 凭据验证通过", started)
		}
	}
	started = time.Now()
	probe, cancel = context.WithCancel(ctx)
	ssh, e := transport.Dial(probe, r.SSH)
	if e != nil {
		cancel()
		add("ssh_identity", "failed", e.Error(), started)
		add("real_data", "skipped", "SSH/SFTP 未通过，无法执行文件读取", time.Now())
	} else {
		add("ssh_identity", "ok", "SSH 主机密钥、公钥认证与 SFTP 子系统通过", started)
		if client != nil && httpsReady && r.ManifestID != "" {
			started = time.Now()
			dataCtx, dataCancel := context.WithTimeout(ctx, 10*time.Minute)
			defer dataCancel()
			published, e := client.Manifest(dataCtx, r.ShareID, r.ManifestID)
			if e == nil && len(published.Manifest.Chunks) > 0 {
				chunk := published.Manifest.Chunks[0]
				hash := sha256.New()
				var n int64
				n, e = ssh.ReadRange(dataCtx, published.SFTPPath, chunk.Offset, chunk.Length, hash)
				report.DataBytes = n
				if e == nil && (n != chunk.Length || hex.EncodeToString(hash.Sum(nil)) != chunk.SHA256) {
					e = fmt.Errorf("真实范围读取长度或 SHA-256 不一致")
				}
			}
			if e != nil {
				add("real_data", "failed", e.Error(), started)
			} else if len(published.Manifest.Chunks) == 0 {
				add("real_data", "skipped", "空文件没有数据块，未声明传输字节已验证", started)
			} else {
				report.TransferVerified = true
				add("real_data", "ok", fmt.Sprintf("真实 SFTP 读取第 0 块，共 %d 字节，与可信清单 SHA-256 一致", report.DataBytes), started)
			}
		} else {
			add("real_data", "skipped", "需要通过 HTTPS 授权并指定清单；端口与认证通过不代表文件字节已验证", time.Now())
		}
		_ = ssh.Close()
		cancel()
	}
	return report
}
