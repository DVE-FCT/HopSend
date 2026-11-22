package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestBuiltAgentAndCLIProcesses(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	// The pure-Go SQLite driver increases cold compilation/link time on Windows.
	// This budget covers building both executables; HTTP readiness stays 10 s.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cliPath := filepath.Join(work, "hopsend-cli.exe")
	agentPath := filepath.Join(work, "hopsend-agent.exe")
	for _, item := range []struct{ path, pkg string }{{cliPath, "./apps/cli"}, {agentPath, "./apps/agent"}} {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", item.path, item.pkg)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, output)
		}
	}
	share := filepath.Join(work, "shared")
	if err = os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(share, "process.txt"), []byte("process smoke"), 0600); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(work, "identity")
	if output, err := exec.CommandContext(ctx, cliPath, "agent-init", "--dir", identity, "--share-dir", share).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, output)
	}
	configPath := filepath.Join(identity, "agent.local.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg httpapi.Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = listener.Addr().String()
	_ = listener.Close()
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	server := exec.CommandContext(ctx, agentPath, "--config", configPath)
	var log bytes.Buffer
	server.Stdout = &log
	server.Stderr = &log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	c, err := nodeclient.New(nodeclient.Config{URL: "https://" + cfg.Listen, CACertificate: filepath.Join(identity, "ca.crt"), TokenFile: filepath.Join(identity, "client.token"), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = c.Health(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("compiled Agent did not become ready:", err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	output, err := exec.CommandContext(ctx, cliPath, "agent-status", "--url", "https://"+cfg.Listen, "--ca-cert", filepath.Join(identity, "ca.crt"), "--token-file", filepath.Join(identity, "client.token"), "--share", "files").CombinedOutput()
	if err != nil {
		t.Fatalf("compiled CLI: %v %s", err, output)
	}
	var result struct {
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		Directory struct {
			Entries []struct {
				Name string `json:"name"`
			} `json:"entries"`
		} `json:"directory"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.Health.Status != "ok" || len(result.Directory.Entries) != 1 || result.Directory.Entries[0].Name != "process.txt" {
		t.Fatalf("unexpected process response: %s", output)
	}
	t.Log("compiled Agent and CLI communicated over real authenticated HTTPS; shared file metadata verified")
}
