// Package sftp implements authenticated, host-verified range reads over SSH/SFTP.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"slices"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Address        string
	User           string
	PrivateKeyPath string
	KnownHostsPath string
	Timeout        time.Duration
}

// Client owns one SSH connection. Cancelling its Dial context closes all its reads.
// Use a separate Client per independently cancellable worker.
type Client struct {
	ssh  *ssh.Client
	sftp *pkgsftp.Client
	stop func() bool
}

func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.User == "" || cfg.PrivateKeyPath == "" || cfg.KnownHostsPath == "" {
		return nil, errors.New("必须配置 SSH 用户、私钥和已核对的 known_hosts")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("SSH 地址必须是 host:port")
	}
	key, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("读取 SSH 私钥: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("解析 SSH 私钥（加密私钥暂不支持交互解锁）: %w", err)
	}
	hostKeys, err := knownhosts.NewDB(cfg.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("读取主机信任记录: %w", err)
	}
	// Servers such as Windows OpenSSH offer several host-key types. Negotiate
	// only algorithms represented by this host's verified trust records, rather
	// than choosing a different (untrusted) key and failing the callback later.
	// Never enable legacy algorithms merely because they exist in known_hosts.
	var hostAlgorithms []string
	supported := ssh.SupportedAlgorithms().HostKeys
	for _, algorithm := range hostKeys.HostKeyAlgorithms(cfg.Address) {
		if slices.Contains(supported, algorithm) {
			hostAlgorithms = append(hostAlgorithms, algorithm)
		}
	}
	if len(hostAlgorithms) == 0 {
		return nil, errors.New("目标主机没有已核对且受支持的主机密钥，请先验证 known_hosts")
	}
	conn, err := (&net.Dialer{Timeout: cfg.Timeout}).DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("连接 SSH: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	success := false
	defer func() {
		if !success {
			stop()
			_ = conn.Close()
		}
	}()
	deadline := time.Now().Add(cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	sshConn, chans, requests, err := ssh.NewClientConn(conn, cfg.Address, &ssh.ClientConfig{
		User: cfg.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeys.HostKeyCallback(), HostKeyAlgorithms: hostAlgorithms,
	})
	if err != nil {
		return nil, contextError(ctx, fmt.Errorf("SSH 握手或认证: %w", err))
	}
	sshClient := ssh.NewClient(sshConn, chans, requests)
	fileClient, err := pkgsftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, contextError(ctx, fmt.Errorf("启动 SFTP: %w", err))
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		_ = fileClient.Close()
		_ = sshClient.Close()
		return nil, err
	}
	success = true
	return &Client{ssh: sshClient, sftp: fileClient, stop: stop}, nil
}

func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) Close() error {
	c.stop()
	fileErr := c.sftp.Close()
	sshErr := c.ssh.Close()
	return errors.Join(fileErr, sshErr)
}

// ReadRange streams exactly length bytes. A truncated source is an error.
func (c *Client) ReadRange(ctx context.Context, path string, offset, length int64, output io.Writer) (int64, error) {
	if offset < 0 || length <= 0 || offset > math.MaxInt64-length {
		return 0, errors.New("分块偏移/长度无效或溢出")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// SFTP has no per-request context; closing this worker's connection unblocks I/O.
	stop := context.AfterFunc(ctx, func() { _ = c.ssh.Close() })
	defer stop()
	file, err := c.sftp.Open(path)
	if err != nil {
		return 0, contextError(ctx, fmt.Errorf("打开远程文件: %w", err))
	}
	defer file.Close()
	// A bounded 1 MiB buffer allows SFTP ReadAt to pipeline packet requests on
	// higher-latency links. SectionReader still enforces the exact byte range.
	n, err := io.CopyBuffer(output, io.NewSectionReader(file, offset, length), make([]byte, 1<<20))
	if err == nil && n != length {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return n, contextError(ctx, fmt.Errorf("读取指定范围: %w", err))
	}
	if err = ctx.Err(); err != nil {
		return n, err
	}
	return n, nil
}
