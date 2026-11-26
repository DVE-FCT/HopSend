package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestDesktopBrowseUseCaseTraversesAuthenticatedAgentAndRejectsChanges(t *testing.T) {
	f := startAgent(t)
	dir := filepath.Join(f.config.Shares[0].Path, "目录")
	if err := os.MkdirAll(filepath.Join(dir, "空目录"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1005; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), []byte{byte(i)}, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := nodeclient.Config{URL: f.address, CACertificate: f.ca, TokenFile: f.token}
	shares := app.BrowseShares(t.Context(), cfg)
	if shares.Error != nil || len(shares.Shares) != 1 || shares.Shares[0].ID != "files" {
		t.Fatal(shares)
	}
	seen := make(map[string]bool)
	cursor := ""
	for {
		result := app.BrowseEntries(t.Context(), cfg, "files", "目录", cursor, 100)
		if result.Error != nil || result.Page == nil {
			t.Fatal(result)
		}
		for _, entry := range result.Page.Entries {
			if seen[entry.Name] {
				t.Fatal("duplicate", entry.Name)
			}
			seen[entry.Name] = true
		}
		if !result.Page.HasMore {
			break
		}
		cursor = result.Page.NextCursor
	}
	if len(seen) != 1006 || !seen["item-0000.txt"] || !seen["item-1004.txt"] || !seen["空目录"] {
		t.Fatal("incomplete browse", len(seen))
	}
	empty := app.BrowseEntries(t.Context(), cfg, "files", "目录/空目录", "", 100)
	if empty.Error != nil || empty.Page == nil || empty.Page.Entries == nil || len(empty.Page.Entries) != 0 || empty.Page.HasMore {
		t.Fatal(empty)
	}
	first := app.BrowseEntries(t.Context(), cfg, "files", "目录", "", 100)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := app.BrowseEntries(t.Context(), cfg, "files", "目录", first.Page.NextCursor, 100)
	if stale.Error == nil || stale.Error.Code != "cursor_stale" || stale.Page != nil {
		t.Fatal("stale page returned partial data", stale)
	}
	for _, path := range []string{"../outside", "C:/outside", "a\\b", "/absolute"} {
		result := app.BrowseEntries(t.Context(), cfg, "files", path, "", 100)
		if result.Error == nil || result.Error.Code != "invalid_browse_request" || result.Page != nil {
			t.Fatal(path, result)
		}
	}
	denied := app.BrowseEntries(t.Context(), cfg, "private", ".", "", 100)
	if denied.Error == nil || denied.Error.Code != "browse_denied" || denied.Page != nil {
		t.Fatal(denied)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := app.BrowseShares(ctx, cfg); result.Error == nil {
		t.Fatal("cancel ignored")
	}
}

func TestBrowseDoesNotGrantDirectoryReadToUploadOnlyCredential(t *testing.T) {
	f := startAgentConfigured(t, func(cfg *httpapi.Config) {
		cfg.Shares[0].Writable = true
		cfg.Credentials[0].ShareIDs = nil
		cfg.Credentials[0].UploadShareIDs = []string{"files"}
		cfg.StateDirectory = filepath.Join(filepath.Dir(cfg.Shares[0].Path), "state")
	})
	cfg := nodeclient.Config{URL: f.address, CACertificate: f.ca, TokenFile: f.token}
	shares := app.BrowseShares(t.Context(), cfg)
	if shares.Error != nil || len(shares.Shares) != 1 || shares.Shares[0].Access != "write_only" {
		t.Fatal(shares)
	}
	page := app.BrowseEntries(t.Context(), cfg, "files", ".", "", 100)
	if page.Error == nil || page.Error.Code != "browse_denied" || page.Page != nil {
		t.Fatal("write-only credential listed directory", page)
	}
}
