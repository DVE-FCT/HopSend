//go:build windows

package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
)

func TestObserveManagedDeviceOnlyPerformsAuthenticatedHTTPSHealth(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","version":"fixture"}`))
	}))
	defer server.Close()
	directory := t.TempDir()
	ca, token := filepath.Join(directory, "ca.crt"), filepath.Join(directory, "client.token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	manager := app.NewManager(filepath.Join(directory, "state.db"), nil)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	a := &App{ctx: t.Context(), manager: manager, devices: []Device{{ID: "managed", AgentURL: server.URL, CAFile: ca, TokenFile: token, ManageAgent: true, SSHAddress: "invalid", RemoteAgentConfig: "missing"}}}
	observation, err := a.ObserveDevice("managed")
	if err != nil || !observation.Reachable || observation.AgentVersion != "fixture" || observation.ObservedAt == "" {
		t.Fatal(observation, err)
	}
	if len(requests) != 1 || <-requests != "GET /v1/health" {
		t.Fatal("observation issued requests other than a read-only health probe")
	}
	if err := os.WriteFile(token, []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	observation, err = a.ObserveDevice("managed")
	if err != nil || observation.Reachable || observation.Detail == "" {
		t.Fatal("unauthorized health probe accepted", observation, err)
	}
}

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
