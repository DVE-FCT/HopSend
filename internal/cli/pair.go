package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/DVE-FCT/HopSend/internal/pairing/material"
)

func pairError(out io.Writer, err error) int {
	var detail *material.Error
	if !errors.As(err, &detail) {
		detail = &material.Error{Stage: "配对资料", Code: "operation_failed", Message: "操作未完成"}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			detail.Code, detail.Message = "cancelled", "操作已取消或超时"
		}
	}
	_ = writeJSON(out, struct {
		Error *material.Error `json:"error"`
	}{detail})
	return 1
}

func runPair(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		return 2
	}
	flags := flag.NewFlagSet("pair "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	switch args[0] {
	case "inspect-request":
		path := flags.String("request", "", "公开配对请求 JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" {
			return 2
		}
		request, err := material.ParseRequestFile(*path)
		if err != nil {
			return pairError(stderr, err)
		}
		if err := writeJSON(out, request); err != nil {
			return pairError(stderr, err)
		}
		return 0
	case "preview":
		path := flags.String("reply", "", "仅预览配对包，不授权或保存")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" {
			return 2
		}
		data, err := material.ReadPackage(*path)
		if err != nil {
			return pairError(stderr, err)
		}
		preview, err := material.PreviewPackage(data)
		if err != nil {
			return pairError(stderr, err)
		}
		if err := writeJSON(out, preview); err != nil {
			return pairError(stderr, err)
		}
		return 0
	case "request":
		trust := flags.String("trust-dir", "", "本账号专用信任目录；不覆盖已有请求")
		source := flags.String("source-ip", "", "已核对的本机 Tailscale IPv4")
		name := flags.String("name", "", "本机显示名称")
		output := flags.String("out-dir", "", "可选，导出公开请求的已有目录")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *trust == "" || *source == "" {
			return 2
		}
		result, err := material.CreateRequest(ctx, material.RequestOptions{TrustDirectory: *trust, SourceIP: *source, Name: *name, OutputDirectory: *output})
		if err != nil {
			return pairError(stderr, err)
		}
		if err := writeJSON(out, result); err != nil {
			return pairError(stderr, err)
		}
		return 0
	case "inspect", "import", "package":
		reply := flags.String("reply", "", "接收设备返回的私密回应 ZIP")
		ssh := flags.String("expected-ssh", "", "通过可信渠道核对的 SSH 主机 SHA-256 指纹")
		ca := flags.String("expected-ca", "", "通过可信渠道核对的 CA 文件 SHA-256")
		request := flags.String("request-id", "", "可选，绑定本次请求 ID；在线配对应提供")
		receiver := flags.String("receiver-ip", "", "可选，绑定目标 Tailscale 地址；在线配对应提供")
		trust := ""
		destination := ""
		if args[0] == "package" {
			flags.StringVar(&destination, "output", "", "新的 .hopsend-pair 输出文件")
		}
		if args[0] == "import" {
			flags.StringVar(&trust, "trust-dir", "", "保存本机原请求和私钥的信任目录")
		}
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *reply == "" || *ssh == "" || *ca == "" || (args[0] == "import" && trust == "") || (args[0] == "package" && destination == "") {
			return 2
		}
		expected := material.Expected{SSHFingerprint: *ssh, CASHA256: *ca, RequestID: *request, ReceiverIP: *receiver}
		if err := ctx.Err(); err != nil {
			return pairError(stderr, err)
		}
		if args[0] == "package" {
			data, err := material.ReadPackage(*reply)
			if err != nil {
				return pairError(stderr, err)
			}
			preview, err := material.ExportPackage(ctx, data, expected, destination)
			if err != nil {
				return pairError(stderr, err)
			}
			if err := writeJSON(out, preview); err != nil {
				return pairError(stderr, err)
			}
			return 0
		}
		if args[0] == "inspect" {
			result, err := material.VerifyReplyFile(*reply, expected)
			if err != nil {
				return pairError(stderr, err)
			}
			// Only public metadata is returned. In particular, files containing
			// credentials and private keys are never encoded into this result.
			if err := writeJSON(out, map[string]any{"device": result.Device, "materials_verified": true, "verification": "not_run"}); err != nil {
				return pairError(stderr, err)
			}
			return 0
		}
		result, err := material.ImportReplyFile(ctx, trust, *reply, expected)
		if err != nil {
			return pairError(stderr, err)
		}
		if err := writeJSON(out, result); err != nil {
			return pairError(stderr, err)
		}
		return 0
	default:
		return 2
	}
}
