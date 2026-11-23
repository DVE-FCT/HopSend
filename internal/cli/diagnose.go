package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runDiagnose(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	f.SetOutput(stderr)
	address := f.String("address", "", "SSH host:port")
	user := f.String("user", "", "SSH 专用用户")
	key := f.String("key", "", "SSH 私钥文件引用")
	known := f.String("known-hosts", "", "已核对主机记录")
	url := f.String("url", "", "Agent HTTPS 地址")
	ca := f.String("ca-cert", "", "受信任 CA 公钥文件")
	token := f.String("token-file", "", "API 凭据文件引用")
	share := f.String("share", "files", "共享 ID")
	manifest := f.String("manifest", "", "可选：读取第 0 块并校验；会使用实际网络流量")
	timeout := f.Duration("timeout", 11*time.Minute, "整个诊断上限；大块中继读取可能较慢")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || *address == "" || *url == "" || *timeout <= 0 {
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	r := app.Diagnose(ctx, app.DownloadRequest{Agent: nodeclient.Config{URL: *url, CACertificate: *ca, TokenFile: *token}, SSH: transport.Config{Address: *address, User: *user, PrivateKeyPath: *key, KnownHostsPath: *known}, ShareID: *share, ManifestID: *manifest})
	if err := writeJSON(out, r); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, l := range r.Layers {
		if l.State == "failed" {
			return 1
		}
	}
	return 0
}
