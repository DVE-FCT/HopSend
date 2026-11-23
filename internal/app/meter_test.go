package app

import (
	"github.com/DVE-FCT/HopSend/internal/engine"
	"path/filepath"
	"testing"
	"time"
)

func TestMeterCountsPayloadSeparatelyFromRecheckedProgress(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := newTransferMeter()
	m.now = func() time.Time { return clock }
	s := TaskStatus{Progress: engine.Progress{State: "rechecking", TotalBytes: 2000}}
	m.progress(s)
	s.State = "downloading"
	s.VerifiedBytes = 1000
	m.progress(s)
	clock = clock.Add(time.Second)
	m.add(100)
	v := m.decorate(s)
	if v.TransferredBytes != 100 || v.ETASeconds != 0 || v.SpeedBytesPerSecond != 100 {
		t.Fatal(v)
	}
	s.VerifiedBytes = 1100
	m.progress(s)
	v = m.decorate(s)
	if v.ETASeconds != 9 {
		t.Fatal(v)
	}
	// Retry rechecks durable bytes; those bytes are never new effective progress.
	s.State = "rechecking"
	m.progress(s)
	s.State = "downloading"
	m.progress(s)
	clock = clock.Add(time.Second)
	m.add(100)
	v = m.decorate(s)
	if v.TransferredBytes != 200 || v.VerifiedBytes != 1100 || v.ETASeconds != 0 {
		t.Fatal(v)
	}
	clock = clock.Add(6 * time.Second)
	v = m.decorate(s)
	if v.SpeedBytesPerSecond != 0 || v.ETASeconds != 0 {
		t.Fatal("idle rate remains live", v)
	}
	for _, state := range []string{"waiting_network", "paused", "verifying", "completed", "cancelled", "failed"} {
		s.State = state
		v = m.decorate(s)
		if v.SpeedBytesPerSecond != 0 || v.ETASeconds != 0 {
			t.Fatal(v)
		}
	}
}

func TestStatusLogPersistsAcrossManagerInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := newEventLog(path)
	s := TaskStatus{Progress: engine.Progress{TaskID: "fixture", State: "paused"}, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	first.append(s)
	first.append(s)
	second := newEventLog(path)
	rows, err := second.read()
	if err != nil || len(rows) != 1 || rows[0].State != "paused" {
		t.Fatal(rows, err)
	}
}
