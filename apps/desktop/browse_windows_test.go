//go:build windows

package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/app"
)

func TestDesktopBrowseBridgeIsReadOnlyAndPreservesStructuredErrors(t *testing.T) {
	directory := t.TempDir()
	requests := []string{}
	mode := "normal"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			if mode == "old" {
				_, _ = w.Write([]byte(`{"api_version":"v1","operations":["shares","list_entries"],"data_transport":"external_sftp","upload_supported":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"api_version":"v1","operations":["shares","list_entries_paginated"],"data_transport":"external_sftp","upload_supported":false}`))
		case "/v1/shares":
			if mode == "duplicate" {
				_, _ = w.Write([]byte(`{"shares":[{"id":"files","access":"read_only"},{"id":"files","access":"read_only"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"shares":[{"id":"files","access":"read_only"}]}`))
		case "/v1/shares/files/entries":
			if r.URL.Query().Get("cursor") == "stale" {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"error":{"code":"cursor_stale","message":"目录已变化"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"share_id":"files","path":".","entries":[{"name":"中文.txt","directory":false,"size_bytes":7}],"next_cursor":"","has_more":false}`))
		default:
			t.Error("unexpected write or management request", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ca, token := filepath.Join(directory, "ca.crt"), filepath.Join(directory, "client.token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	manager := app.NewManager(filepath.Join(directory, "state.db"), nil)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	a := &App{ctx: t.Context(), directory: directory, manager: manager, devices: []Device{{ID: "managed", Name: "fixture", AgentURL: server.URL, CAFile: ca, TokenFile: token, ManageAgent: true, SSHAddress: "invalid", RemoteAgentConfig: "missing"}}}
	before := append([]Device{}, a.devices...)
	shares, err := a.BrowseDeviceShares("managed")
	if err != nil || shares.Error != nil || len(shares.Shares) != 1 {
		t.Fatal(shares, err)
	}
	page, err := a.BrowseDeviceEntries("managed", "files", ".", "", 100)
	if err != nil || page.Error != nil || page.Page == nil || len(page.Page.Entries) != 1 || page.Page.Entries[0].Name != "中文.txt" {
		t.Fatal(page, err)
	}
	stale, err := a.BrowseDeviceEntries("managed", "files", ".", "stale", 100)
	if err != nil || stale.Error == nil || stale.Error.Code != "cursor_stale" || stale.Page != nil {
		t.Fatal(stale, err)
	}
	mode = "old"
	unsupported, err := a.BrowseDeviceShares("managed")
	if err != nil || unsupported.Error == nil || unsupported.Error.Code != "browse_unsupported" {
		t.Fatal(unsupported, err)
	}
	mode = "duplicate"
	duplicate, err := a.BrowseDeviceShares("managed")
	if err != nil || duplicate.Error == nil || duplicate.Error.Code != "invalid_shares" || len(duplicate.Shares) != 0 {
		t.Fatal(duplicate, err)
	}
	if !reflect.DeepEqual(a.devices, before) {
		t.Fatal("read-only browsing edited devices")
	}
	if _, err := os.Stat(filepath.Join(directory, "devices.json")); !os.IsNotExist(err) {
		t.Fatal("browse wrote device snapshot")
	}
	for _, request := range requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatal("browse issued non-read request", request)
		}
	}
	if _, err := a.BrowseDeviceShares("unknown"); err == nil {
		t.Fatal("unknown device accepted")
	}
}
