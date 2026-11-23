package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/app"
)

func TestLocalBaselineVerifiesFilesAndDoesNotReuseOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "synthetic.bin")
	data := bytes.Repeat([]byte("HopSend-基准-"), 160000)
	if e := os.WriteFile(source, data, 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(root, "measurements")
	cfg := Config{Source: source, Repeats: 2, SyncChunkMiB: 1}
	report, e := Run(context.Background(), cfg, app.DownloadRequest{}, output, nil)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(data)
	if !report.Complete || report.ContentID != hex.EncodeToString(h[:]) || len(report.Samples) != 4 {
		t.Fatal(report)
	}
	for _, s := range report.Samples {
		if !s.Verified || s.Bytes != int64(len(data)) || s.Seconds <= 0 || s.MiBPerSecond <= 0 {
			t.Fatal(s)
		}
	}
	disk, e := os.ReadFile(filepath.Join(output, "report.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved Report
	if json.Unmarshal(disk, &saved) != nil || !saved.Complete {
		t.Fatal(string(disk))
	}
	entries, e := os.ReadDir(output)
	if e != nil || len(entries) != 1 {
		t.Fatal("generated disk files were not removed", e)
	}
	if _, e = Run(context.Background(), cfg, app.DownloadRequest{}, output, nil); e == nil {
		t.Fatal("reused existing output")
	}
	if actual, _ := os.ReadFile(source); !bytes.Equal(actual, data) {
		t.Fatal("source changed")
	}
}

func TestNetworkBudgetStopsBeforeDial(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if e := os.WriteFile(source, bytes.Repeat([]byte{23}, 1024), 0600); e != nil {
		t.Fatal(e)
	}
	cfg := Config{Source: source, Repeats: 1, SyncChunkMiB: 1, Cases: []Case{{ManifestID: string(bytes.Repeat([]byte{'a'}, 64)), Workers: 1}}, TrafficBudgetBytes: 1024}
	report, e := Run(context.Background(), cfg, app.DownloadRequest{}, filepath.Join(root, "out"), nil)
	if e == nil || report.Complete || len(report.Samples) != 2 || report.EstimatedPayload != 3072 {
		t.Fatal(report, e)
	}
	for _, s := range report.Samples {
		if s.PayloadBytes != 0 {
			t.Fatal("unexpected traffic")
		}
	}
}

func TestSummaryMedianAndFailedSamples(t *testing.T) {
	samples := []Sample{{Kind: "a", MiBPerSecond: 30, Verified: true}, {Kind: "a", MiBPerSecond: 10, Verified: true}, {Kind: "a", MiBPerSecond: 1000, Verified: false}}
	v := summarize(samples)
	if len(v) != 1 || v[0].Median != 20 || v[0].Count != 2 || v[0].Min != 10 || v[0].Max != 30 {
		t.Fatal(v)
	}
}
