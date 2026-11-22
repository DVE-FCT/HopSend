package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/cli"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/security"
)

type agentFixture struct {
	address, ca, token, dir string
	server                  *httptest.Server
	config                  httpapi.Config
}

func startAgent(t *testing.T) agentFixture {
	return startAgentConfigured(t, nil)
}

func startAgentConfigured(t *testing.T, configure func(*httpapi.Config)) agentFixture {
	t.Helper()
	root := t.TempDir()
	share := filepath.Join(root, "shared")
	if err := os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "中文样例.txt"), []byte("HopSend"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "identity")
	var out, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"agent-init", "--dir", dir, "--share-dir", share}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if code := cli.Run(context.Background(), []string{"agent-init", "--dir", dir}, &out, &stderr); code == 0 {
		t.Fatal("identity overwritten")
	}
	cfg, err := httpapi.LoadConfig(filepath.Join(dir, "agent.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.Shares = append(cfg.Shares, httpapi.Share{ID: "private", Path: private})
	otherToken := filepath.Join(dir, "other.token")
	if err := os.WriteFile(otherToken, []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Credentials = append(cfg.Credentials, httpapi.Credential{ID: "other", TokenFile: otherToken, ShareIDs: []string{"private"}})
	if configure != nil {
		configure(&cfg)
	}
	srv, err := httpapi.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(cfg.CertificateFile, cfg.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv.HTTP.Handler)
	ts.TLS = srv.HTTP.TLSConfig.Clone()
	ts.TLS.Certificates = []tls.Certificate{pair}
	ts.StartTLS()
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })
	return agentFixture{address: ts.URL, ca: filepath.Join(dir, "ca.crt"), token: cfg.Credentials[0].TokenFile, dir: dir, server: ts, config: cfg}
}

func (f agentFixture) client(t *testing.T) *nodeclient.Client {
	t.Helper()
	c, err := nodeclient.New(nodeclient.Config{URL: f.address, CACertificate: f.ca, TokenFile: f.token, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestAgentTLSAuthScopeAndCLI(t *testing.T) {
	f := startAgent(t)
	c := f.client(t)
	ctx := context.Background()
	health, err := c.Health(ctx)
	if err != nil || health.Status != "ok" || health.APIVersion != "v1" {
		t.Fatal(health, err)
	}
	caps, err := c.Capabilities(ctx)
	if err != nil || caps.UploadSupported || caps.DataTransport != "external_sftp" {
		t.Fatal(caps, err)
	}
	shares, err := c.Shares(ctx)
	if err != nil || len(shares.Shares) != 1 || shares.Shares[0].ID != "files" {
		t.Fatal(shares, err)
	}
	entries, err := c.Entries(ctx, "files", ".")
	if err != nil || len(entries.Entries) != 1 || entries.Entries[0].Name != "中文样例.txt" || entries.Entries[0].SizeBytes != 7 {
		t.Fatal(entries, err)
	}
	if _, err = c.Entries(ctx, "private", "."); err == nil {
		t.Fatal("unauthorized share listed")
	}
	if _, err = c.Entries(ctx, "missing", "."); err == nil {
		t.Fatal("unknown share listed")
	}
	other := f
	other.token = f.config.Credentials[1].TokenFile
	otherShares, err := other.client(t).Shares(ctx)
	if err != nil || len(otherShares.Shares) != 1 || otherShares.Shares[0].ID != "private" {
		t.Fatal(otherShares, err)
	}
	var out, stderr bytes.Buffer
	if code := cli.Run(ctx, []string{"agent-status", "--url", f.address, "--ca-cert", f.ca, "--token-file", f.token, "--share", "files"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if strings.Contains(out.String(), f.dir) || strings.Contains(out.String(), "BEGIN PRIVATE KEY") {
		t.Fatal("sensitive output")
	}
	var value map[string]any
	if err = json.Unmarshal(out.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	t.Log("real TLS 1.3 + credential scope + CLI directory query passed")
}

func TestAgentRejectsUnknownCAAndBadCredentials(t *testing.T) {
	f := startAgent(t)
	t.Run("unknown CA", func(t *testing.T) {
		bundle, err := security.NewTLSBundle([]string{"localhost"})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "other.crt")
		if err = os.WriteFile(path, bundle.CA, 0600); err != nil {
			t.Fatal(err)
		}
		bad := f
		bad.ca = path
		c := bad.client(t)
		if _, err = c.Health(context.Background()); err == nil {
			t.Fatal("unknown certificate trusted")
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wrong.token")
		_ = os.WriteFile(path, []byte(strings.Repeat("f", 64)), 0600)
		bad := f
		bad.token = path
		if _, err := bad.client(t).Health(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatal(err)
		}
	})
	t.Run("missing token", func(t *testing.T) {
		response, err := f.server.Client().Get(f.address + "/v1/health")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal(response.Status)
		}
	})
}

func TestAgentRejectsPathTraversal(t *testing.T) {
	f := startAgent(t)
	c := f.client(t)
	for _, path := range []string{"../private", "/", "C:/Windows", `..\private`, "nested/../../private", ".\x00"} {
		if _, err := c.Entries(context.Background(), "files", path); err == nil {
			t.Fatal("path accepted", path)
		}
	}
}

func TestAgentRejectsDirectoryLink(t *testing.T) {
	f := startAgent(t)
	link := filepath.Join(f.config.Shares[0].Path, "escape")
	err := os.Symlink(f.config.Shares[1].Path, link)
	if err != nil && runtime.GOOS == "windows" {
		// NTFS junction creation does not require the symlink privilege.
		_, err = exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, f.config.Shares[1].Path).CombinedOutput()
	}
	if err != nil {
		t.Skip("directory-link creation unavailable:", err)
	}
	if _, err := f.client(t).Entries(context.Background(), "files", "escape"); err == nil {
		t.Fatal("directory-link traversal accepted")
	}
}

func TestAgentRejectsPlainHTTPAndWrongMethods(t *testing.T) {
	f := startAgent(t)
	data, err := os.ReadFile(f.token)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		method, path string
		want         int
	}{{"POST", "/v1/health", 405}, {"GET", "/v1/unknown", 404}} {
		r, err := http.NewRequest(item.method, f.address+item.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(data)))
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != item.want {
			t.Fatal(response.Status, string(body))
		}
	}
	if _, err := nodeclient.New(nodeclient.Config{URL: "http://localhost:9443", CACertificate: f.ca, TokenFile: f.token}); err == nil {
		t.Fatal("plaintext client configured")
	}
	// Verify os.Root policy also refuses reserved Windows-style paths before I/O.
	if _, err := f.client(t).Entries(context.Background(), "files", "file.txt:stream"); err == nil {
		t.Fatal("alternate data stream accepted")
	}
}

func TestAgentConfigRejectsUnknownFieldsAndInvalidScope(t *testing.T) {
	f := startAgent(t)
	bad := f.config
	bad.Credentials = append([]httpapi.Credential(nil), f.config.Credentials...)
	bad.Credentials[0].ShareIDs = []string{"nonexistent"}
	if server, err := httpapi.New(bad); err == nil {
		server.Close()
		t.Fatal("unknown share scope configured")
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte(`{"listen":"127.0.0.1:1","unknown":true}`), 0600)
	if _, err := httpapi.LoadConfig(path); err == nil {
		t.Fatal("unknown configuration accepted")
	}
}
