package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/DVE-FCT/HopSend/configs"
)

type Tool struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual,omitempty"`
	Status   string `json:"status"`
}

type Report struct {
	Scope string `json:"scope"`
	Ready bool   `json:"ready"`
	Tools []Tool `json:"tools"`
}

type Runner func(context.Context, string, ...string) (string, error)

var semanticVersion = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

func Execute(ctx context.Context, name string, args ...string) (string, error) {
	data, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(data), err
}

func Doctor(ctx context.Context, run Runner) (Report, error) {
	var expected map[string]string
	if err := json.Unmarshal(configs.ToolchainJSON, &expected); err != nil {
		return Report{}, fmt.Errorf("读取工具链契约: %w", err)
	}
	report := Report{Scope: "development_toolchain", Ready: true, Tools: make([]Tool, 0, 4)}
	for _, name := range []string{"go", "node", "pnpm", "wails"} {
		args := []string{"--version"}
		if name == "go" || name == "wails" {
			args = []string{"version"}
		}
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		output, err := run(probe, name, args...)
		cancel()
		tool := Tool{Name: name, Expected: expected[name]}
		if err != nil {
			tool.Status = "unavailable"
		} else {
			// Go prints go1.x.y; word-boundary matching needs the prefix removed.
			tool.Actual = semanticVersion.FindString(strings.ReplaceAll(output, "go1.", "1."))
			tool.Status = "version_mismatch"
			if tool.Actual == tool.Expected {
				tool.Status = "ok"
			}
		}
		if tool.Status != "ok" {
			report.Ready = false
		}
		report.Tools = append(report.Tools, tool)
	}
	return report, ctx.Err()
}
