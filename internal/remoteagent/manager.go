// Package remoteagent prepares already paired Windows devices over host-verified
// SSH. It never installs services, changes firewall rules, or replaces trust.
package remoteagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

type Request struct {
	SSH                         transport.Config
	Agent                       nodeclient.Config
	ConfigPath, Binary, ShareID string
}
type Step struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}
type Report struct {
	Ready      bool   `json:"ready"`
	ObservedAt string `json:"observed_at"`
	Managed    bool   `json:"managed"`
	Steps      []Step `json:"steps"`
}
type lease struct {
	gate    chan struct{}
	client  *transport.Client
	process *transport.Command
}
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	leases map[string]*lease
}

func NewManager(parent context.Context) *Manager {
	ctx, cancel := context.WithCancel(parent)
	return &Manager{ctx: ctx, cancel: cancel, leases: make(map[string]*lease)}
}

var Default = NewManager(context.Background())

// Ensure checks an existing Agent before each transfer attempt. An unavailable
// managed Agent goes through deployment, identity checks and a real data probe.
func (m *Manager) Ensure(ctx context.Context, r Request) error {
	c, err := nodeclient.New(r.Agent)
	if err != nil {
		return err
	}
	defer c.Close()
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, err = c.Health(probe)
	cancel()
	if err == nil {
		return nil
	}
	if trustFailure(err) {
		return err
	}
	_, err = m.Prepare(ctx, r)
	return err
}

func (m *Manager) Prepare(ctx context.Context, r Request) (Report, error) {
	ctx, cancelPreparation := context.WithCancel(ctx)
	stopManager := context.AfterFunc(m.ctx, cancelPreparation)
	defer func() { stopManager(); cancelPreparation() }()
	report := Report{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Steps: []Step{}}
	add := func(name, detail string) { report.Steps = append(report.Steps, Step{name, "ok", detail}) }
	fail := func(name string, err error) (Report, error) {
		report.Steps = append(report.Steps, Step{name, "failed", err.Error()})
		return report, err
	}
	if r.ConfigPath == "" {
		return fail("configuration", errors.New("尚未完成设备准备：需要一次性导入远端 Agent 配置路径与已核对的配对文件"))
	}
	host, _, addressErr := net.SplitHostPort(r.SSH.Address)
	agentURL, urlErr := url.Parse(r.Agent.URL)
	if addressErr != nil || urlErr != nil || agentURL.Scheme != "https" || agentURL.User != nil || !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(agentURL.Hostname(), ".")) {
		return fail("configuration", errors.New("自动准备要求 Agent HTTPS 与 SSH 使用同一配对设备地址"))
	}
	key := r.SSH.Address + "\x00" + r.SSH.User + "\x00" + r.ConfigPath + "\x00" + r.Agent.URL
	m.mu.Lock()
	l := m.leases[key]
	if l == nil {
		l = &lease{gate: make(chan struct{}, 1)}
		m.leases[key] = l
	}
	m.mu.Unlock()
	select {
	case l.gate <- struct{}{}:
		defer func() { <-l.gate }()
	case <-ctx.Done():
		return fail("preparation", ctx.Err())
	case <-m.ctx.Done():
		return fail("preparation", m.ctx.Err())
	}
	c, err := transport.Dial(ctx, r.SSH)
	if err != nil {
		return fail("ssh", err)
	}
	defer c.Close()
	add("ssh", "SSH 主机密钥、公钥认证和 SFTP 子系统通过")
	data, err := c.RunCommand(ctx, powershell(environmentScript, map[string]string{"config": r.ConfigPath}))
	if err != nil {
		return fail("environment", fmt.Errorf("检查远端配对环境失败：%w；%s", err, string(data)))
	}
	var env struct{ Root, Config, Platform string }
	if json.Unmarshal(data, &env) != nil || env.Root == "" || env.Config == "" || env.Platform != "windows-x64" {
		return fail("environment", errors.New("远端环境响应无效"))
	}
	add("environment", "Windows x64 环境与原配对配置存在，保留原证书和权限")
	client, err := nodeclient.New(r.Agent)
	if err != nil {
		return fail("https", err)
	}
	defer client.Close()
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, healthErr := client.Health(probe)
	cancel()
	if healthErr != nil {
		if trustFailure(healthErr) {
			return fail("https", fmt.Errorf("HTTPS 身份或授权失败，不自动替换信任：%w", healthErr))
		}
		if l.client != nil {
			_ = l.client.Close()
			l.client = nil
			l.process = nil
		}
		executable, hash, err := deploy(ctx, c, r.Binary, env.Root)
		if err != nil {
			return fail("deployment", err)
		}
		add("deployment", "Agent 程序已就位，远端 SHA-256 与本机程序一致")
		lifetime, cancelRun := context.WithCancel(m.ctx)
		stop := context.AfterFunc(ctx, cancelRun)
		owned, err := transport.Dial(lifetime, r.SSH)
		if err != nil {
			stop()
			cancelRun()
			return fail("agent", err)
		}
		process, err := owned.StartCommand(lifetime, powershell(runScript, map[string]string{"executable": executable, "sha256": hash, "config": env.Config}))
		if err != nil {
			stop()
			cancelRun()
			_ = owned.Close()
			return fail("agent", err)
		}
		deadline := time.NewTimer(25 * time.Second)
		ticker := time.NewTicker(400 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for {
			probe, cancel = context.WithTimeout(ctx, 2*time.Second)
			_, healthErr = client.Health(probe)
			cancel()
			if healthErr == nil {
				break
			}
			if trustFailure(healthErr) {
				break
			}
			if process.Exited() {
				output, exitErr := process.Wait(ctx)
				healthErr = fmt.Errorf("Agent 启动退出：%v；%s", exitErr, string(output))
				break
			}
			select {
			case <-ctx.Done():
				healthErr = ctx.Err()
			case <-deadline.C:
				healthErr = errors.New("Agent 启动后 HTTPS 未就绪，请检查监听地址或防火墙")
			case <-ticker.C:
				continue
			}
			break
		}
		if healthErr != nil {
			stop()
			cancelRun()
			_ = owned.Close()
			return fail("agent", healthErr)
		}
		if !stop() || ctx.Err() != nil {
			cancelRun()
			_ = owned.Close()
			return fail("agent", ctx.Err())
		}
		l.client, l.process = owned, process
		report.Managed = true
		add("agent", "Agent 已启动并通过 HTTPS；应用持有受认证会话，失效时下次连接自动恢复")
	} else {
		report.Managed = l.client != nil && l.process != nil && !l.process.Exited()
		add("agent", "已有 Agent 的 HTTPS 健康检查通过，复用现有进程")
	}
	shares, err := client.Shares(ctx)
	if err != nil {
		return fail("https", err)
	}
	found := false
	for _, share := range shares.Shares {
		if share.ID == r.ShareID {
			found = true
		}
	}
	if !found {
		return fail("https", errors.New("当前 API 凭据未授权所选共享"))
	}
	add("https", "HTTPS 证书、API 凭据及所选共享授权通过")
	if err = probeData(ctx, c, env.Root); err != nil {
		return fail("data", err)
	}
	add("data", "实际 SFTP 写入并读回 32 KiB，SHA-256 一致；测试文件已清理")
	report.Ready = true
	return report, nil
}

func trustFailure(err error) bool {
	var a x509.UnknownAuthorityError
	var b x509.HostnameError
	var c *tls.CertificateVerificationError
	var d *nodeclient.HTTPError
	return errors.As(err, &a) || errors.As(err, &b) || errors.As(err, &c) || (errors.As(err, &d) && d.StatusCode >= 400 && d.StatusCode < 500)
}

func deploy(ctx context.Context, c *transport.Client, binary, root string) (string, string, error) {
	f, err := os.Open(binary)
	if err != nil {
		return "", "", errors.New("缺少配套 Agent 程序，请使用完整 HopSend 目录或重新构建桌面端")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 2 || info.Size() > 128<<20 {
		return "", "", errors.New("Agent 程序尺寸或类型无效")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", "", err
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return "", "", err
	}
	directory := path.Join(root, "versions", sum)
	if err = c.MakeDirectory(directory); err != nil {
		return "", "", err
	}
	exe := path.Join(directory, "hopsend-agent.exe")
	verified := func() error {
		output, err := c.RunCommand(ctx, powershell(verifyScript, map[string]string{"executable": exe, "sha256": sum}))
		if err != nil {
			return err
		}
		var value struct{ Verified bool }
		if json.Unmarshal(output, &value) != nil || !value.Verified {
			return errors.New("Agent 程序校验响应无效")
		}
		return nil
	}
	if err = verified(); err == nil {
		return exe, sum, nil
	}
	var suffix [16]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		return "", "", err
	}
	temporary := path.Join(directory, "upload-"+hex.EncodeToString(suffix[:])+".tmp")
	if err = c.PutNew(ctx, temporary, f, info.Size()); err != nil {
		return "", "", fmt.Errorf("部署 Agent 失败（不覆盖已有程序）：%w", err)
	}
	defer c.RemovePreparationFile(temporary)
	output, err := c.RunCommand(ctx, powershell(publishScript, map[string]string{"temporary": temporary, "executable": exe, "sha256": sum}))
	if err != nil {
		return "", "", fmt.Errorf("发布 Agent 程序失败：%w；%s", err, string(output))
	}
	if err = verified(); err != nil {
		return "", "", fmt.Errorf("部署后的 Agent 哈希校验失败：%w", err)
	}
	return exe, sum, nil
}

func probeData(ctx context.Context, c *transport.Client, root string) error {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	directory := path.Join(root, "probes")
	if err := c.MakeDirectory(directory); err != nil {
		return err
	}
	file := path.Join(directory, hex.EncodeToString(suffix[:])+".bin")
	data := make([]byte, 32<<10)
	if _, err := rand.Read(data); err != nil {
		return err
	}
	if err := c.PutNew(ctx, file, bytes.NewReader(data), int64(len(data))); err != nil {
		return err
	}
	cleaned := false
	defer func() {
		if !cleaned {
			_ = c.RemovePreparationFile(file)
		}
	}()
	hash := sha256.New()
	n, err := c.ReadRange(ctx, file, 0, int64(len(data)), hash)
	if err != nil {
		return err
	}
	expected := sha256.Sum256(data)
	if n != int64(len(data)) || !bytes.Equal(hash.Sum(nil), expected[:]) {
		return errors.New("SFTP 回读长度或 SHA-256 不一致")
	}
	if err := c.RemovePreparationFile(file); err != nil {
		return err
	}
	cleaned = true
	return nil
}

func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.leases {
		select {
		case l.gate <- struct{}{}:
			if l.client != nil {
				_ = l.client.Close()
			}
			<-l.gate
		default:
		}
	}
}
