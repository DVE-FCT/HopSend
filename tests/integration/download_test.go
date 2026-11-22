package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/cli"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestTrustedHTTPSManifestToRealSFTPFileDownload(t *testing.T) {
	ssh, source, _ := startSFTP(t, 0)
	dir := filepath.Dir(ssh.PrivateKeyPath)
	store := filepath.Join(dir, "content")
	m, err := manifest.Publish(context.Background(), filepath.Join(dir, "sample.bin"), store, "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", m.ContentID), 0600) })
	agent := startAgentConfigured(t, func(cfg *httpapi.Config) { cfg.ContentDirectory = store })
	node := nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token, Timeout: 2 * time.Second}
	c, err := nodeclient.New(node)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Manifest(context.Background(), "private", m.ManifestID); err == nil {
		t.Fatal("cross-share manifest exposed")
	}
	database := filepath.Join(t.TempDir(), "tasks.sqlite")
	request := app.DownloadRequest{Agent: node, SSH: ssh, ShareID: "files", ManifestID: m.ManifestID, Destination: filepath.Join(t.TempDir(), "download.bin"), Workers: 2, Database: database}
	result, err := app.Download(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(source, actual) {
		t.Fatal("download does not match real source", err)
	}
	var output, stderr bytes.Buffer
	args := []string{"download", "--url", agent.address, "--ca-cert", agent.ca, "--token-file", agent.token, "--manifest", m.ManifestID, "--share", "files", "--address", ssh.Address, "--user", ssh.User, "--key", ssh.PrivateKeyPath, "--known-hosts", ssh.KnownHostsPath, "--output", filepath.Join(t.TempDir(), "cli-download.bin"), "--quiet"}
	args = append(args, "--database", database)
	if code := cli.Run(context.Background(), args, &output, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	t.Log("actual HTTPS manifest + two SSH/SFTP workers + persisted file + independent full hash verification passed")
}
