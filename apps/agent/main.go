package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
)

func main() { os.Exit(run()) }
func run() int {
	flags := flag.NewFlagSet("hopsend-agent", flag.ContinueOnError)
	configPath := flags.String("config", "", "Agent 配置 JSON 路径")
	if err := flags.Parse(os.Args[1:]); err != nil || flags.NArg() != 0 || *configPath == "" {
		fmt.Fprintln(os.Stderr, "用法：hopsend-agent --config agent.local.json")
		return 2
	}
	cfg, err := httpapi.LoadConfig(*configPath)
	if err != nil {
		slog.Error("配置读取失败", "error", err)
		return 1
	}
	server, err := httpapi.New(cfg)
	if err != nil {
		slog.Error("Agent 初始化失败", "error", err)
		return 1
	}
	defer server.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	done := make(chan struct{})
	defer close(done)
	shutdownComplete := make(chan struct{})
	go func() {
		defer close(shutdownComplete)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.HTTP.Shutdown(shutdown)
		case <-done:
		}
	}()
	slog.Info("HopSend HTTPS Agent 启动", "listen", cfg.Listen, "api_version", "v1")
	err = server.HTTP.ListenAndServeTLS(cfg.CertificateFile, cfg.KeyFile)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownComplete
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("Agent 退出", "error", err)
		return 1
	}
	return 0
}
