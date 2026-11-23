//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/engine"
)

func TestRevealTargetOnlyAcceptsExistingCompletedLocalDownloads(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "中文,下载.bin")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{file, dir} {
		task := engine.Task{State: "completed", Destination: target, Source: []byte(`{"Direction":"download"}`)}
		if actual, err := localTaskTarget(task); err != nil || actual != target {
			t.Fatal(actual, err)
		}
	}
	for _, task := range []engine.Task{
		{State: "paused", Destination: file, Source: []byte(`{}`)},
		{State: "completed", Destination: file, Source: []byte(`{"Direction":"upload"}`)},
		{State: "completed", Destination: "https://remote/shares/a/b", Source: []byte(`{}`)},
		{State: "completed", Destination: "relative.bin", Source: []byte(`{}`)},
		{State: "completed", Destination: filepath.Join(dir, "missing"), Source: []byte(`{}`)},
		{State: "completed", Destination: file, Source: []byte(`invalid`)},
	} {
		if _, err := localTaskTarget(task); err == nil {
			t.Fatal("invalid reveal target accepted", task)
		}
	}
}
