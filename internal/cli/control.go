package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

// Control is an explicitly interactive local session. It never sends console
// text to SSH; commands operate on the same in-process manager as the desktop.
func runControl(ctx context.Context, args []string, input io.Reader, out, stderr io.Writer) int {
	f := flag.NewFlagSet("control", flag.ContinueOnError)
	f.SetOutput(stderr)
	database := f.String("database", app.DefaultDatabase(), "SQLite 任务数据库")
	id := f.String("task", "", "需要继续的任务 ID")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || !manifest.ValidID(*id) {
		return 2
	}
	var mu sync.Mutex
	write := func(v any) { mu.Lock(); defer mu.Unlock(); _ = json.NewEncoder(out).Encode(v) }
	m := app.NewManager(*database, func(s app.TaskStatus) { write(s) })
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = m.Close(stop)
	}()
	if _, err := m.Resume(ctx, *id); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stderr, "本地控制：pause 暂停，resume 继续，cancel 取消，status 状态，quit 暂停并退出。")
	scanner := bufio.NewScanner(input)
	commands := make(chan string)
	scanDone := make(chan error, 1)
	stopScan := make(chan struct{})
	defer close(stopScan)
	go func() {
		for scanner.Scan() {
			select {
			case commands <- scanner.Text():
			case <-stopScan:
				return
			}
		}
		scanDone <- scanner.Err()
	}()
	for {
		var command string
		select {
		case <-ctx.Done():
			return 0
		case err := <-scanDone:
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		case command = <-commands:
		}
		var err error
		switch command {
		case "pause":
			err = m.Pause(ctx, *id)
		case "resume":
			_, err = m.Resume(ctx, *id)
		case "cancel":
			err = m.Cancel(ctx, *id)
		case "status":
			s, _ := m.Status(*id)
			write(s)
		case "quit":
			return 0
		default:
			err = fmt.Errorf("未知本地控制命令")
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
	}
}
