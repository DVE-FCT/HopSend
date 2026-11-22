package diagnostics

import (
	"context"
	"errors"
	"testing"
)

func TestDoctorClassifiesMissingAndMismatch(t *testing.T) {
	versions := map[string]string{"go": "go version go1.27.4 windows/amd64", "node": "v24.14.1", "pnpm": "10.0.0"}
	report, err := Doctor(context.Background(), func(_ context.Context, name string, args ...string) (string, error) {
		if output, ok := versions[name]; ok {
			return output, nil
		}
		return "", errors.New("not found")
	})
	if err != nil || report.Ready {
		t.Fatalf("unexpected report: %+v %v", report, err)
	}
	want := []string{"ok", "ok", "version_mismatch", "unavailable"}
	for i, tool := range report.Tools {
		if tool.Status != want[i] {
			t.Errorf("%s: got %s want %s", tool.Name, tool.Status, want[i])
		}
	}
}

func TestDoctorAllPinnedToolsReady(t *testing.T) {
	versions := map[string]string{"go": "go version go1.27.4 windows/amd64", "node": "v24.14.1", "pnpm": "11.25.0", "wails": "v2.17.1"}
	r, err := Doctor(context.Background(), func(_ context.Context, name string, _ ...string) (string, error) { return versions[name], nil })
	if err != nil || !r.Ready {
		t.Fatalf("expected ready: %+v %v", r, err)
	}
}
