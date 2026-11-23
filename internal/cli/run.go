package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/DVE-FCT/HopSend/internal/buildinfo"
	"github.com/DVE-FCT/HopSend/internal/diagnostics"
)

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func Run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "跃传 HopSend\n用法：hopsend-cli version | doctor [--strict] | diagnose [参数] | sftp-read [参数] | agent-init [参数] | agent-status [参数] | publish [参数] | download [参数] | upload [参数] | tasks [--database 路径] | resume --task ID [--database 路径] | control --task ID [--database 路径]\nsftp-read 只诊断范围；download 可信下载；upload 等待接收端校验发布；resume 重核验并恢复原任务；control 在本进程内暂停、继续和取消。")
		return 0
	}
	switch args[0] {
	case "diagnose":
		return runDiagnose(ctx, args[1:], out, stderr)
	case "upload":
		return runUpload(ctx, args[1:], out, stderr)
	case "control":
		return runControl(ctx, args[1:], os.Stdin, out, stderr)
	case "tasks":
		return runTasks(ctx, args[1:], out, stderr, false)
	case "resume":
		return runTasks(ctx, args[1:], out, stderr, true)
	case "publish":
		return runPublish(ctx, args[1:], out, stderr)
	case "download":
		return runDownload(ctx, args[1:], out, stderr)
	case "agent-init":
		return runAgentInit(args[1:], out, stderr)
	case "agent-status":
		return runAgentStatus(ctx, args[1:], out, stderr)
	case "sftp-read":
		return runSFTP(ctx, args[1:], out, stderr)
	case "version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "version 不接受参数")
			return 2
		}
		if err := writeJSON(out, map[string]string{"name": "HopSend", "version": buildinfo.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "doctor":
		flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
		flags.SetOutput(stderr)
		strict := flags.Bool("strict", false, "工具缺失或版本不符时返回非零退出码")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return 2
		}
		report, err := diagnostics.Doctor(ctx, diagnostics.Execute)
		if err == nil {
			err = writeJSON(out, report)
		}
		if err != nil {
			fmt.Fprintln(stderr, "诊断失败:", err)
			return 1
		}
		if *strict && !report.Ready {
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, "未知命令:", args[0])
		return 2
	}
}
