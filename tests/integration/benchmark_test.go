package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/benchmark"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

func TestBenchmarkUsesRealHTTPSAndSFTPWithoutResuming(t *testing.T) {
	ssh, data, _ := startSFTP(t, 0)
	dir := filepath.Dir(ssh.PrivateKeyPath)
	content := filepath.Join(dir, "content")
	m, e := manifest.Publish(context.Background(), filepath.Join(dir, "sample.bin"), content, "files", manifest.MinChunkSize)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(content, "objects", m.ContentID), 0600) })
	agent := startAgentConfigured(t, func(c *httpapi.Config) { c.ContentDirectory = content })
	device := app.DownloadRequest{Agent: nodeclient.Config{URL: agent.address, CACertificate: agent.ca, TokenFile: agent.token}, SSH: ssh, ShareID: "files"}
	plan := benchmark.Config{Source: filepath.Join(dir, "sample.bin"), Repeats: 1, SyncChunkMiB: 1, Cases: []benchmark.Case{{ManifestID: m.ManifestID, Workers: 2}}, TrafficBudgetBytes: 3 * int64(len(data))}
	report, e := benchmark.Run(context.Background(), plan, device, filepath.Join(t.TempDir(), "results"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if !report.Complete || len(report.Samples) != 5 {
		t.Fatal(report)
	}
	last := report.Samples[len(report.Samples)-1]
	if last.Kind != "engine_download" || last.PayloadBytes != int64(len(data)) || last.SHA256 != m.ContentID || !last.Verified {
		t.Fatal(last)
	}
	// A different local content cannot silently label remote bytes as a match.
	other := filepath.Join(dir, "different.bin")
	if e = os.WriteFile(other, []byte("different"), 0600); e != nil {
		t.Fatal(e)
	}
	plan.Source = other
	if report, e = benchmark.Run(context.Background(), plan, device, filepath.Join(t.TempDir(), "rejected"), nil); e == nil || report.Complete {
		t.Fatal("mismatching manifest accepted")
	}
}
