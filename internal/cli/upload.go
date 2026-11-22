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
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runUpload(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("upload", flag.ContinueOnError)
	f.SetOutput(stderr)
	url := f.String("url", "", "接收 Agent HTTPS 地址")
	ca := f.String("ca-cert", "", "CA 公钥路径")
	token := f.String("token-file", "", "API 凭据路径")
	address := f.String("address", "", "接收端 SSH host:port")
	user := f.String("user", "", "SSH 专用账号")
	key := f.String("key", "", "私钥路径")
	known := f.String("known-hosts", "", "已核对主机记录")
	share := f.String("share", "inbox", "获上传授权的共享 ID")
	source := f.String("source", "", "本机源文件")
	target := f.String("target", "", "远端单级文件名，留空沿用源文件名")
	database := f.String("database", app.DefaultDatabase(), "SQLite 任务数据库")
	workers := f.Int("workers", 2, "活动分块数")
	chunk := f.Int64("chunk-mib", 32, "逻辑块 MiB，1–64")
	retries := f.Int("retries", 6, "临时网络重试次数")
	timeout := f.Duration("read-timeout", 2*time.Minute, "持续无数据进展的超时")
	quiet := f.Bool("quiet", false, "关闭 stderr 分块进度")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || *source == "" || *workers < 1 || *workers > 8 || *chunk < 1 || *chunk > 64 || *timeout <= 0 {
		return 2
	}
	r := app.DownloadRequest{Agent: nodeclient.Config{URL: *url, CACertificate: *ca, TokenFile: *token}, SSH: transport.Config{Address: *address, User: *user, PrivateKeyPath: *key, KnownHostsPath: *known}, ShareID: *share, Database: *database, Workers: *workers, Retries: *retries, ReadTimeout: *timeout}
	var err error
	r, err = app.PrepareUpload(ctx, r, *source, *target, *chunk<<20)
	if err != nil {
		fmt.Fprintln(stderr, "创建上传版本失败:", err)
		return 1
	}
	if !*quiet {
		encoder := json.NewEncoder(stderr)
		r.OnProgress = func(p engine.Progress) { _ = encoder.Encode(p) }
	}
	result, err := app.Upload(ctx, r)
	if err == nil {
		err = writeJSON(out, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, "上传未完成（以接收端确认为准）:", err)
		return 1
	}
	return 0
}
