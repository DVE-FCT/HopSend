package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/cli"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/publication"
)

func TestPublishedPathIndexBindsAnImmutableVersion(t *testing.T) {
	f := startAgentConfigured(t, func(cfg *httpapi.Config) {
		cfg.ContentDirectory = filepath.Join(filepath.Dir(cfg.Shares[0].Path), "content")
		if err := os.MkdirAll(cfg.ContentDirectory, 0700); err != nil {
			t.Fatal(err)
		}
	})
	share := f.config.Shares[0].Path
	content := f.config.ContentDirectory
	if err := os.MkdirAll(filepath.Dir(content), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(share, "发布", "报告.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("版本一"), 0600); err != nil {
		t.Fatal(err)
	}
	client := f.client(t)
	_, err := client.PathIndex(context.Background(), "files", "发布/报告.txt")
	var httpErr *nodeclient.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || httpErr.Code != "path_unpublished" {
		t.Fatalf("unpublished path: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "agent.json")
	configData, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	var publishOut, publishErr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"publish-path", "--agent-config", configPath, "--share", "files", "--path", "发布/报告.txt", "--chunk-mib", "1"}, &publishOut, &publishErr); code != 0 {
		t.Fatalf("publish-path CLI: %d %s", code, publishErr.String())
	}
	var first v1.PathVersion
	if err := json.Unmarshal(publishOut.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	got, err := client.PathIndex(context.Background(), "files", "发布/报告.txt")
	if err != nil || got.ManifestID != first.ManifestID || got.ContentID != first.ContentID || got.SizeBytes != 9 || got.PayloadKind != "file" {
		t.Fatalf("published path result: %+v, %v", got, err)
	}
	if strings.Contains(got.Path, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
		t.Fatal("path index leaked an absolute filesystem path")
	}
	if err := os.WriteFile(path, []byte("版本二"), 0600); err != nil {
		t.Fatal(err)
	}
	still, err := client.PathIndex(context.Background(), "files", "发布/报告.txt")
	if err != nil || still.ManifestID != first.ManifestID || still.ContentID != first.ContentID {
		t.Fatalf("live source change silently changed immutable index: %+v, %v", still, err)
	}
	second, err := publication.Publish(context.Background(), share, content, "files", "发布/报告.txt", 64<<10)
	if err != nil || second.ManifestID == first.ManifestID || second.ContentID == first.ContentID {
		t.Fatalf("republish did not create a new version: %+v, %v", second, err)
	}
	latest, err := client.PathIndex(context.Background(), "files", "发布/报告.txt")
	if err != nil || latest.ManifestID != second.ManifestID {
		t.Fatalf("index did not atomically move to new version: %+v, %v", latest, err)
	}
	if err := os.Remove(filepath.Join(content, "objects", second.ContentID)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PathIndex(context.Background(), "files", "发布/报告.txt"); !errors.As(err, &httpErr) || httpErr.StatusCode != 409 || httpErr.Code != "index_stale" {
		t.Fatalf("missing published object was not rejected: %v", err)
	}
	if _, err := client.PathIndex(context.Background(), "files", "../报告.txt"); err == nil {
		t.Fatal("path traversal accepted")
	}
	t.Logf("published path index bound explicit versions %s -> %s", first.ManifestID, second.ManifestID)
}

func TestPublishedPathIndexDoesNotGrantWriteOnlyAccess(t *testing.T) {
	f := startAgentConfigured(t, func(cfg *httpapi.Config) {
		cfg.ContentDirectory = filepath.Join(filepath.Dir(cfg.Shares[0].Path), "content")
		if err := os.MkdirAll(cfg.ContentDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		cfg.Shares[0].Writable = true
		cfg.Credentials[0].ShareIDs = nil
		cfg.Credentials[0].UploadShareIDs = []string{"files"}
		cfg.StateDirectory = filepath.Join(filepath.Dir(cfg.Shares[0].Path), "state")
	})
	share := f.config.Shares[0].Path
	content := f.config.ContentDirectory
	if err := os.MkdirAll(filepath.Dir(content), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "write-only.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publication.Publish(context.Background(), share, content, "files", "write-only.txt", 64<<10); err != nil {
		t.Fatal(err)
	}
	other := f
	other.token = f.config.Credentials[0].TokenFile
	client := other.client(t)
	if _, err := client.PathIndex(context.Background(), "files", "write-only.txt"); err == nil {
		t.Fatal("write-only credential accessed path index")
	}
}
