package integration

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/app"
)

func TestLayeredDiagnosticsReadsActualSFTPAndSeparatesAPITrustFailure(t *testing.T) {
	r, _, _ := controlledDownloadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := app.Diagnose(ctx, r)
	states := map[string]string{}
	for _, l := range report.Layers {
		states[l.Name] = l.State
	}
	if !report.TransferVerified || report.DataBytes <= 0 || states["https_identity"] != "ok" || states["ssh_identity"] != "ok" || states["real_data"] != "ok" {
		t.Fatal(report)
	}
	// A healthy TCP endpoint does not excuse an untrusted HTTPS identity.
	r.Agent.CACertificate = r.SSH.KnownHostsPath
	report = app.Diagnose(ctx, r)
	for _, l := range report.Layers {
		states[l.Name] = l.State
	}
	if report.TransferVerified || states["https_port"] != "ok" || states["https_identity"] != "failed" || states["ssh_identity"] != "ok" || states["real_data"] != "skipped" {
		t.Fatal(report)
	}
}

func TestRealUploadPayloadCounterAndCompletedRecheck(t *testing.T) {
	r, data, inbox := uploadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	observedRate, observedETA := false, false
	m := app.NewManager(r.Database, func(s app.TaskStatus) {
		if s.State == "uploading" && s.SpeedBytesPerSecond > 0 {
			observedRate = true
		}
		if s.State == "uploading" && s.ETASeconds > 0 {
			observedETA = true
		}
	})
	defer m.Close(context.Background())
	id, err := m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Status(id)
	if !observedRate || !observedETA {
		t.Fatal("real SFTP upload never produced an active rate and ETA", observedRate, observedETA)
	}
	if !s.MetricsAvailable || s.TransferredBytes != int64(len(data)) || s.VerifiedBytes != int64(len(data)) || s.SpeedBytesPerSecond != 0 || s.ETASeconds != 0 {
		t.Fatal(s)
	}
	if b, err := os.ReadFile(inbox + string(os.PathSeparator) + "received.bin"); err != nil || !bytes.Equal(b, data) {
		t.Fatal(err)
	}
	if _, err = m.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Status(id)
	if s.TransferredBytes != 0 || s.VerifiedBytes != int64(len(data)) {
		t.Fatal("recheck counted cached data as payload", s)
	}
	// New manager instances read the same real transition history from disk.
	logs, err := app.NewManager(r.Database, nil).Logs()
	if err != nil || len(logs) == 0 || logs[0].State != "completed" {
		t.Fatal(logs, err)
	}
}
