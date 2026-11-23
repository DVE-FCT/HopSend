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
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/remoteagent"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func runDevicePrepare(ctx context.Context, args []string, out, stderr io.Writer) int {
	f := flag.NewFlagSet("device-prepare", flag.ContinueOnError)
	f.SetOutput(stderr)
	file := f.String("device-config", "", "已配对的设备 JSON（只含文件引用）")
	binary := f.String("agent-binary", app.AgentBinary(), "要部署的配套 Agent 程序")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || *file == "" {
		return 2
	}
	data, err := os.ReadFile(*file)
	if err != nil || len(data) > 1<<20 {
		fmt.Fprintln(stderr, "设备配置不可读或超过 1 MiB")
		return 1
	}
	var d struct {
		AgentURL   string `json:"agent_url"`
		SSHAddress string `json:"ssh_address"`
		SSHUser    string `json:"ssh_user"`
		CA         string `json:"ca_file"`
		Token      string `json:"token_file"`
		Key        string `json:"key_file"`
		Known      string `json:"known_hosts_file"`
		Share      string `json:"share_id"`
		Config     string `json:"remote_agent_config"`
	}
	if err = json.Unmarshal(data, &d); err != nil {
		fmt.Fprintln(stderr, "设备配置 JSON 无效")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	manager := remoteagent.NewManager(ctx)
	defer manager.Close()
	r, err := manager.Prepare(ctx, remoteagent.Request{Agent: nodeclient.Config{URL: d.AgentURL, CACertificate: d.CA, TokenFile: d.Token}, SSH: transport.Config{Address: d.SSHAddress, User: d.SSHUser, PrivateKeyPath: d.Key, KnownHostsPath: d.Known}, ConfigPath: d.Config, Binary: *binary, ShareID: d.Share})
	if jsonErr := writeJSON(out, r); jsonErr != nil {
		fmt.Fprintln(stderr, jsonErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stderr, "准备验收通过。CLI 退出会释放管理连接；桌面端在运行期间持有连接，并在后续连接时自动检查和恢复。")
	return 0
}
