package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/DVE-FCT/HopSend/internal/remoteagent"
)

func AgentBinary() string {
	executable, _ := os.Executable()
	directory := filepath.Dir(executable)
	for _, candidate := range []string{filepath.Join(directory, "hopsend-agent.exe"), filepath.Join(directory, "..", "agent", "hopsend-agent.exe")} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return filepath.Join(directory, "hopsend-agent.exe")
}

func prepareAgent(ctx context.Context, r DownloadRequest) error {
	if r.ManagedAgentConfig == "" {
		return nil
	}
	binary := r.ManagedAgentBinary
	if _, err := os.Stat(binary); err != nil {
		binary = AgentBinary()
	}
	return remoteagent.Default.Ensure(ctx, remoteagent.Request{SSH: r.SSH, Agent: r.Agent, ConfigPath: r.ManagedAgentConfig, Binary: binary, ShareID: r.ShareID})
}
