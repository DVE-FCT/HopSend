package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"time"

	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runSFTP(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("sftp-read", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("address", "", "SSH host:port")
	user := flags.String("user", "", "SSH 用户")
	key := flags.String("key", "", "私钥路径")
	known := flags.String("known-hosts", "", "人工核对指纹后的 known_hosts 路径")
	remote := flags.String("remote", "", "SFTP 路径（由服务端账号权限限制）")
	offset := flags.Int64("offset", 0, "起始字节")
	length := flags.Int64("length", 0, "精确字节数，必须大于 0")
	timeout := flags.Duration("timeout", 30*time.Second, "整个范围请求超时")
	expected := flags.String("sha256", "", "可信来源给出的范围哈希（可选）")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *remote == "" || *length <= 0 || *offset < 0 || *timeout <= 0 {
		fmt.Fprintln(stderr, "缺少远程路径或范围/超时无效")
		return 2
	}
	if *expected != "" {
		decoded, err := hex.DecodeString(*expected)
		if err != nil || len(decoded) != sha256.Size {
			fmt.Fprintln(stderr, "预期 SHA-256 格式无效")
			return 2
		}
	}
	request, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client, err := transport.Dial(request, transport.Config{Address: *address, User: *user, PrivateKeyPath: *key, KnownHostsPath: *known, Timeout: *timeout})
	if err != nil {
		fmt.Fprintln(stderr, "SFTP 连接失败:", err)
		return 1
	}
	defer client.Close()
	hash := sha256.New()
	started := time.Now()
	n, err := client.ReadRange(request, *remote, *offset, *length, hash)
	if err != nil {
		fmt.Fprintln(stderr, "范围读取失败:", err)
		return 1
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if *expected != "" {
		want, _ := hex.DecodeString(*expected)
		if actual != hex.EncodeToString(want) {
			fmt.Fprintln(stderr, "范围 SHA-256 与预期不一致")
			return 1
		}
	}
	if err := writeJSON(out, map[string]any{"protocol": "sftp", "bytes": n, "offset": *offset, "sha256": actual, "expected_hash_verified": *expected != "", "elapsed_ms": time.Since(started).Milliseconds()}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
