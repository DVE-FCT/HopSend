package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/security"
)

func runAgentInit(args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "新的配置目录（必须为空）")
	hosts := flags.String("hosts", "localhost,127.0.0.1", "证书实际主机名/IP，逗号分隔")
	share := flags.String("share-dir", "", "可选，明确授权的只读目录")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || flags.NArg() != 0 {
		return 2
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	bundle, err := security.NewTLSBundle(strings.Split(*hosts, ","))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		fmt.Fprintln(stderr, "配置目录非空，拒绝覆盖已有密钥或配置")
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "无法读取配置目录")
		return 1
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		fmt.Fprintln(stderr, "无法创建配置目录")
		return 1
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg := httpapi.Config{Listen: "127.0.0.1:9443", CertificateFile: "server.crt", KeyFile: "server.key", ContentDirectory: "content", Credentials: []httpapi.Credential{{ID: "local-client", TokenFile: "client.token", ShareIDs: []string{}}}, Shares: []httpapi.Share{}}
	if *share != "" {
		path, err := filepath.Abs(*share)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			fmt.Fprintln(stderr, "共享路径不是已有目录")
			return 1
		}
		cfg.Shares = append(cfg.Shares, httpapi.Share{ID: "files", Path: path})
		cfg.Credentials[0].ShareIDs = []string{"files"}
	}
	configData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	files := []struct {
		name string
		data []byte
	}{{"ca.crt", bundle.CA}, {"server.crt", bundle.Certificate}, {"server.key", bundle.PrivateKey}, {"client.token", []byte(hex.EncodeToString(token) + "\n")}, {"agent.local.json", append(configData, '\n')}}
	for _, item := range files {
		file, err := os.OpenFile(filepath.Join(root, item.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = file.Write(item.data)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			fmt.Fprintln(stderr, "初始化未完成，请检查目标目录（不会覆盖已有文件）")
			return 1
		}
	}
	if err = os.Mkdir(filepath.Join(root, "content"), 0700); err != nil {
		fmt.Fprintln(stderr, "创建内容目录失败")
		return 1
	}
	if err := writeJSON(out, map[string]string{"config": filepath.Join(root, "agent.local.json"), "ca_sha256": bundle.CAFingerprint, "listen": cfg.Listen}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runAgentStatus(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("url", "", "Agent HTTPS 地址")
	ca := flags.String("ca-cert", "", "受信任 CA 公钥文件")
	token := flags.String("token-file", "", "本设备凭据文件")
	share := flags.String("share", "", "可选，列出此共享的目录")
	path := flags.String("path", ".", "共享内相对目录")
	timeout := flags.Duration("timeout", 10*time.Second, "请求超时")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return 2
	}
	c, err := nodeclient.New(nodeclient.Config{URL: *address, CACertificate: *ca, TokenFile: *token, Timeout: *timeout})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer c.Close()
	health, err := c.Health(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	capabilities, err := c.Capabilities(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	shares, err := c.Shares(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := map[string]any{"health": health, "capabilities": capabilities, "shares": shares.Shares}
	if *share != "" {
		entries, err := c.Entries(ctx, *share, *path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result["directory"] = entries
	}
	if err = writeJSON(out, result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
