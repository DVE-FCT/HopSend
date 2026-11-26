package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/publication"
)

func runPublishPath(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("publish-path", flag.ContinueOnError)
	f.SetOutput(stderr)
	config := f.String("agent-config", "", "源设备 Agent 配置路径（本机所有者操作）")
	share := f.String("share", "files", "共享 ID")
	path := f.String("path", "", "共享内相对文件或文件夹路径")
	chunk := f.Int64("chunk-mib", 32, "逻辑分块 MiB，1–64")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if f.NArg() != 0 || *config == "" || publication.ValidatePath(*path) != nil || *chunk < 1 || *chunk > 64 {
		return 2
	}
	cfg, err := httpapi.LoadConfig(*config)
	if err == nil && cfg.ContentDirectory == "" {
		err = fmt.Errorf("Agent 未配置内容库")
	}
	if err == nil {
		found := false
		for _, item := range cfg.Shares {
			if item.ID != *share {
				continue
			}
			if found {
				err = fmt.Errorf("共享 ID 重复")
				break
			}
			found = true
		}
		if !found {
			err = fmt.Errorf("Agent 配置中没有此共享")
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "发布失败:", err)
		return 1
	}
	for _, item := range cfg.Shares {
		if item.ID != *share {
			continue
		}
		value, err := publication.Publish(ctx, item.Path, cfg.ContentDirectory, *share, *path, *chunk<<20)
		if err == nil {
			err = writeJSON(out, value)
		}
		if err != nil {
			fmt.Fprintln(stderr, "发布失败:", err)
			return 1
		}
		return 0
	}
	return 1
}

func runPathIndex(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("path-index", flag.ContinueOnError)
	f.SetOutput(stderr)
	address := f.String("url", "", "Agent HTTPS 地址")
	ca := f.String("ca-cert", "", "受信任 CA 证书")
	token := f.String("token-file", "", "设备凭据路径")
	share := f.String("share", "files", "共享 ID")
	path := f.String("path", "", "共享内相对路径")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if f.NArg() != 0 || publication.ValidatePath(*path) != nil {
		return 2
	}
	c, err := nodeclient.New(nodeclient.Config{URL: *address, CACertificate: *ca, TokenFile: *token, Timeout: 15 * time.Second})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer c.Close()
	value, err := c.PathIndex(ctx, *share, *path)
	if err == nil {
		err = writeJSON(out, value)
	}
	if err != nil {
		fmt.Fprintln(stderr, "查询已发布版本失败:", err)
		return 1
	}
	return 0
}
