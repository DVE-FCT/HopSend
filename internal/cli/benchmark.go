package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/benchmark"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runBenchmark(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	f.SetOutput(stderr)
	plan := f.String("plan", "", "基准计划 JSON：合成源、清单/并发矩阵、重复次数、网络字节预算")
	device := f.String("device-config", "", "已配对设备 JSON，仅网络矩阵需要")
	output := f.String("output", "", "尚不存在的专用输出目录；父目录必须存在")
	timeout := f.Duration("timeout", 30*time.Minute, "整组测量超时上限，最多两小时")
	if e := f.Parse(args); e != nil || f.NArg() != 0 || *plan == "" || *output == "" || *timeout <= 0 || *timeout > 2*time.Hour {
		return 2
	}
	var c benchmark.Config
	if e := readBenchmarkJSON(*plan, &c); e != nil {
		fmt.Fprintln(stderr, e)
		return 2
	}
	if e := benchmark.Validate(c); e != nil {
		fmt.Fprintln(stderr, e)
		return 2
	}
	var r app.DownloadRequest
	if len(c.Cases) > 0 {
		var d struct {
			AgentURL string `json:"agent_url"`
			Address  string `json:"ssh_address"`
			User     string `json:"ssh_user"`
			CA       string `json:"ca_file"`
			Token    string `json:"token_file"`
			Key      string `json:"key_file"`
			Known    string `json:"known_hosts_file"`
			Share    string `json:"share_id"`
		}
		// Device files can also carry UI names/managed configuration. Decode known
		// fields without changing deployment or trust; only explicit plan is strict.
		file, e := os.Open(*device)
		if e != nil {
			fmt.Fprintln(stderr, "需要可读的已配对设备文件")
			return 2
		}
		e = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&d)
		file.Close()
		if e != nil {
			return 2
		}
		r = app.DownloadRequest{Agent: nodeclient.Config{URL: d.AgentURL, CACertificate: d.CA, TokenFile: d.Token}, SSH: transport.Config{Address: d.Address, User: d.User, PrivateKeyPath: d.Key, KnownHostsPath: d.Known}, ShareID: d.Share, ReadTimeout: 2 * time.Minute}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, e := benchmark.Run(ctx, c, r, *output, func(s benchmark.Sample) {
		fmt.Fprintf(stderr, "%s #%d 块=%dMiB 并发=%d %.2fMiB/s 核验=%t\n", s.Kind, s.Iteration, s.ChunkBytes>>20, s.Workers, s.MiBPerSecond, s.Verified)
	})
	if e != nil {
		fmt.Fprintln(stderr, "基准未完成：", e)
		return 1
	}
	if e = writeJSON(out, report); e != nil {
		return 1
	}
	return 0
}
func readBenchmarkJSON(path string, value any) error {
	file, e := os.Open(path)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return e
	}
	if info.Size() > 1<<20 {
		return fmt.Errorf("基准计划超过 1MiB")
	}
	d := json.NewDecoder(file)
	d.DisallowUnknownFields()
	if e = d.Decode(value); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("基准计划必须为单个 JSON 对象")
	}
	return nil
}
