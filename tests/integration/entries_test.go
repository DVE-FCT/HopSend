package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/cli"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestAgentDirectoryPaginationAndStaleCursor(t *testing.T) {
	f := startAgent(t)
	pageDir := filepath.Join(f.config.Shares[0].Path, "many")
	if err := os.Mkdir(pageDir, 0700); err != nil {
		t.Fatal(err)
	}
	const total = 1005
	for i := 0; i < total; i++ {
		name := filepath.Join(pageDir, fmt.Sprintf("item-%04d.bin", i))
		if err := os.WriteFile(name, []byte{byte(i)}, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := f.client(t)
	ctx := context.Background()
	cursor := ""
	seen := make(map[string]bool, total)
	last := ""
	pages := 0
	for {
		page, err := c.EntriesPage(ctx, "files", "many", 173, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Entries) == 0 && page.HasMore {
			t.Fatal("empty page reported more entries")
		}
		for _, entry := range page.Entries {
			if seen[entry.Name] {
				t.Fatalf("duplicate entry %q", entry.Name)
			}
			if last != "" && entry.Name <= last {
				t.Fatalf("entries are not strictly sorted: %q after %q", entry.Name, last)
			}
			seen[entry.Name] = true
			last = entry.Name
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatal("final page returned a cursor")
			}
			break
		}
		if page.NextCursor == "" {
			t.Fatal("non-final page did not return a cursor")
		}
		cursor = page.NextCursor
	}
	if pages != 6 || len(seen) != total {
		t.Fatalf("unexpected pagination result: pages=%d entries=%d", pages, len(seen))
	}
	for i := 0; i < total; i++ {
		if !seen[fmt.Sprintf("item-%04d.bin", i)] {
			t.Fatalf("missing expected entry %d", i)
		}
	}
	for _, limit := range []int{0, 500} {
		page, err := c.EntriesPage(ctx, "files", "many", limit, "")
		expected := limit
		if expected == 0 {
			expected = v1.DefaultEntryPageSize
		}
		if err != nil || len(page.Entries) != expected || !page.HasMore {
			t.Fatalf("page boundary %d: %d entries, err=%v", limit, len(page.Entries), err)
		}
	}
	var out, stderr bytes.Buffer
	if code := cli.Run(ctx, []string{"agent-status", "--url", f.address, "--ca-cert", f.ca, "--token-file", f.token, "--share", "files", "--path", "many", "--limit", "500", "--all"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var cliResult struct {
		Directory v1.EntryList `json:"directory"`
	}
	if err := json.Unmarshal(out.Bytes(), &cliResult); err != nil || len(cliResult.Directory.Entries) != total || cliResult.Directory.HasMore || cliResult.Directory.NextCursor != "" {
		t.Fatalf("CLI aggregation: count=%d err=%v", len(cliResult.Directory.Entries), err)
	}

	changing := filepath.Join(f.config.Shares[0].Path, "changing")
	if err := os.Mkdir(changing, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(changing, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := c.EntriesPage(ctx, "files", "changing", 1, "")
	if err != nil || !first.HasMore || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	if err := os.WriteFile(filepath.Join(changing, "c.txt"), []byte("c"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.EntriesPage(ctx, "files", "changing", 1, first.NextCursor)
	var httpErr *nodeclient.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict || httpErr.Code != "cursor_stale" {
		t.Fatalf("directory change was not reported as stale cursor: %v", err)
	}
	t.Logf("directory pagination passed: %d entries across %d pages; changes require restart", len(seen), pages)
}

func TestAgentDirectoryChangesInvalidateCursor(t *testing.T) {
	for _, change := range []string{"add", "delete", "rename", "resize", "modified_time"} {
		t.Run(change, func(t *testing.T) {
			f := startAgent(t)
			share := f.config.Shares[0].Path
			victim := filepath.Join(share, "z.txt")
			if err := os.WriteFile(victim, []byte("initial"), 0600); err != nil {
				t.Fatal(err)
			}
			c := f.client(t)
			first, err := c.EntriesPage(context.Background(), "files", ".", 1, "")
			if err != nil || !first.HasMore {
				t.Fatal(first, err)
			}
			switch change {
			case "add":
				err = os.WriteFile(filepath.Join(share, "a.txt"), []byte("new"), 0600)
			case "delete":
				err = os.Remove(victim)
			case "rename":
				err = os.Rename(victim, filepath.Join(share, "renamed.txt"))
			case "resize":
				err = os.WriteFile(victim, []byte("different-length"), 0600)
			case "modified_time":
				var info os.FileInfo
				info, err = os.Stat(victim)
				if err == nil {
					err = os.Chtimes(victim, info.ModTime().Add(time.Hour), info.ModTime().Add(time.Hour))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.EntriesPage(context.Background(), "files", ".", 1, first.NextCursor)
			var response *nodeclient.HTTPError
			if !errors.As(err, &response) || response.StatusCode != 409 || response.Code != "cursor_stale" {
				t.Fatalf("mutation %s: %v", change, err)
			}
			restarted, err := c.EntriesAll(context.Background(), "files", ".", 1)
			if err != nil || restarted.HasMore || restarted.NextCursor != "" {
				t.Fatal(restarted, err)
			}
		})
	}
}

func TestAgentDirectoryEmptyAndInvalidRequests(t *testing.T) {
	f := startAgent(t)
	share := f.config.Shares[0].Path
	if err := os.Mkdir(filepath.Join(share, "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	c := f.client(t)
	page, err := c.EntriesPage(context.Background(), "files", "空目录", 1, "")
	if err != nil || page.Entries == nil || len(page.Entries) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	first, err := c.EntriesPage(context.Background(), "files", ".", 1, "")
	if err != nil || !first.HasMore {
		t.Fatal(first, err)
	}
	tampered := strings.Split(first.NextCursor, ".")
	if tampered[1][0] == 'A' {
		tampered[1] = "B" + tampered[1][1:]
	} else {
		tampered[1] = "A" + tampered[1][1:]
	}
	requests := []struct {
		query string
		want  int
		code  string
	}{
		{"limit=0", 400, "invalid_limit"}, {"limit=-1", 400, "invalid_limit"}, {"limit=501", 400, "invalid_limit"},
		{"limit=1.5", 400, "invalid_limit"}, {"limit=abc", 400, "invalid_limit"},
		{"cursor=bad-cursor", 400, "invalid_cursor"},
		{"cursor=" + url.QueryEscape(strings.Join(tampered, ".")), 400, "invalid_cursor"},
		{"path=" + url.QueryEscape("空目录") + "&cursor=" + url.QueryEscape(first.NextCursor), 400, "invalid_cursor"},
		{"path=" + url.QueryEscape("中文样例.txt"), 400, "not_directory"},
	}
	token, err := os.ReadFile(f.token)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range requests {
		r, err := http.NewRequest(http.MethodGet, f.address+"/v1/shares/files/entries?"+item.query, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var detail v1.Error
		err = json.NewDecoder(response.Body).Decode(&detail)
		response.Body.Close()
		if err != nil || response.StatusCode != item.want || detail.Error.Code != item.code {
			t.Fatalf("request %q: HTTP %d code=%q err=%v", item.query, response.StatusCode, detail.Error.Code, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.EntriesPage(ctx, "files", ".", 1, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestAgentDirectoryCursorBoundToShareAndInstance(t *testing.T) {
	configure := func(cfg *httpapi.Config) {
		cfg.Shares = append(cfg.Shares, httpapi.Share{ID: "alias", Path: cfg.Shares[0].Path})
		cfg.Credentials[0].ShareIDs = append(cfg.Credentials[0].ShareIDs, "alias")
	}
	f := startAgentConfigured(t, configure)
	if err := os.WriteFile(filepath.Join(f.config.Shares[0].Path, "another.txt"), []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	c := f.client(t)
	first, err := c.EntriesPage(context.Background(), "files", ".", 1, "")
	if err != nil || !first.HasMore {
		t.Fatal(first, err)
	}
	_, err = c.EntriesPage(context.Background(), "alias", ".", 1, first.NextCursor)
	var response *nodeclient.HTTPError
	if !errors.As(err, &response) || response.Code != "invalid_cursor" {
		t.Fatalf("cursor reused in another authorized share: %v", err)
	}
	// Same TLS identity, credentials and share paths, but a new Agent instance.
	restarted := startAgentConfigured(t, func(cfg *httpapi.Config) { *cfg = f.config })
	restarted.ca = f.ca
	_, err = restarted.client(t).EntriesPage(context.Background(), "files", ".", 1, first.NextCursor)
	if !errors.As(err, &response) || response.Code != "invalid_cursor" {
		t.Fatalf("old instance cursor survived restart: %v", err)
	}
}
