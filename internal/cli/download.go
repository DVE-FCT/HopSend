package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runPublish(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("publish", flag.ContinueOnError)
	f.SetOutput(stderr)
	source := f.String("source", "", "源端常规文件")
	store := f.String("content-dir", "", "Agent content_directory 的实际路径")
	share := f.String("share", "files", "授权共享 ID")
	chunk := f.Int64("chunk-mib", 32, "逻辑分块 MiB，1–64")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if f.NArg() != 0 || *source == "" || *store == "" || *chunk < 1 || *chunk > 64 {
		return 2
	}
	m, err := manifest.Publish(ctx, *source, *store, *share, *chunk<<20)
	if err != nil {
		fmt.Fprintln(stderr, "发布内容失败:", err)
		return 1
	}
	if err = writeJSON(out, m); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runDownload(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("download", flag.ContinueOnError)
	f.SetOutput(stderr)
	url := f.String("url", "", "Agent HTTPS 根地址")
	ca := f.String("ca-cert", "", "受信任 CA 公钥路径")
	token := f.String("token-file", "", "此设备的凭据文件")
	id := f.String("manifest", "", "已发布清单 ID")
	share := f.String("share", "files", "共享 ID")
	address := f.String("address", "", "SSH host:port")
	user := f.String("user", "", "SSH 专用账号")
	key := f.String("key", "", "SSH 私钥路径")
	known := f.String("known-hosts", "", "已核对的主机记录")
	destination := f.String("output", "", "目标文件路径；现有文件不会覆盖")
	workers := f.Int("workers", 2, "每节点并发块数，1–8")
	timeout := f.Duration("read-timeout", 2*time.Minute, "分块持续无数据进展的超时")
	quiet := f.Bool("quiet", false, "关闭 stderr 分块进度")
	database := f.String("database", app.DefaultDatabase(), "SQLite 任务数据库路径")
	retries := f.Int("retries", 6, "临时网络错误的重试次数，0–100")
	retryDelay := f.Duration("retry-delay", time.Second, "初始网络退避，最多一分钟")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if f.NArg() != 0 || *destination == "" || *workers < 1 || *workers > 8 || *timeout <= 0 || !manifest.ValidID(*id) {
		return 2
	}
	var progress func(engine.Progress)
	if !*quiet {
		encoder := json.NewEncoder(stderr)
		progress = func(p engine.Progress) { _ = encoder.Encode(p) }
	}
	r, err := app.Download(ctx, app.DownloadRequest{Agent: nodeclient.Config{URL: *url, CACertificate: *ca, TokenFile: *token}, SSH: transport.Config{Address: *address, User: *user, PrivateKeyPath: *key, KnownHostsPath: *known}, ShareID: *share, ManifestID: *id, Destination: *destination, Workers: *workers, ReadTimeout: *timeout, OnProgress: progress, Database: *database, Retries: *retries, RetryDelay: *retryDelay})
	if err != nil {
		fmt.Fprintln(stderr, "下载未完成:", err)
		return 1
	}
	if err = writeJSON(out, r); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runTasks(ctx context.Context, args []string, out, stderr io.Writer, resume bool) int {
	f := flag.NewFlagSet("tasks", flag.ContinueOnError)
	f.SetOutput(stderr)
	database := f.String("database", app.DefaultDatabase(), "SQLite 任务数据库路径")
	id := f.String("task", "", "任务 ID，resume 必填")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || (resume && !manifest.ValidID(*id)) {
		return 2
	}
	var value any
	var err error
	if resume {
		encoder := json.NewEncoder(stderr)
		value, err = app.Resume(ctx, *database, *id, func(p engine.Progress) { _ = encoder.Encode(p) })
	} else {
		var store *sqlite.Store
		store, err = sqlite.Open(*database)
		if err == nil {
			var tasks []engine.Task
			tasks, err = store.List(ctx)
			_ = store.Close()
			summaries := make([]map[string]any, 0, len(tasks))
			for _, t := range tasks {
				summaries = append(summaries, map[string]any{"id": t.ID, "manifest_id": t.Manifest.ManifestID, "destination": t.Destination, "state": t.State, "confirmed_chunks": len(t.Confirmed), "total_chunks": len(t.Manifest.Chunks), "total_bytes": t.Manifest.SizeBytes, "updated_at": t.UpdatedAt})
			}
			value = summaries
		}
	}
	if err == nil {
		err = writeJSON(out, value)
	}
	if err != nil {
		fmt.Fprintln(stderr, "任务操作失败:", err)
		return 1
	}
	return 0
}
