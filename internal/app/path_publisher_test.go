package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
)

func TestSourcePublisherConfigBindingAndLifetime(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "shared")
	store := filepath.Join(base, "content")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "中文.txt"), []byte("publication"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(base, "agent.json")
	data, err := json.Marshal(httpapi.Config{Shares: []httpapi.Share{{ID: "files", Path: root}}, ContentDirectory: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	review, err := ReadPublicationConfig(config)
	if err != nil || len(review.Shares) != 1 {
		t.Fatal(review, err)
	}
	p := &PathPublisher{}
	if _, err := p.Start(t.Context(), config, "changed", "files", "中文.txt"); err == nil {
		t.Fatal("changed configuration accepted")
	}
	job, err := p.Start(t.Context(), config, review.Revision, "files", "中文.txt")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		status := p.Status()
		if status.State != "publishing" {
			if status.State != "completed" || status.Version == nil || status.ID != job.ID {
				t.Fatal(status)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("publication did not finish")
		case <-time.After(5 * time.Millisecond):
		}
	}
	v := p.Status().Version
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", v.ContentID), 0600) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Start(ctx, config, review.Revision, "files", "中文.txt"); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.Status().State != "cancelled" {
		t.Fatal(p.Status())
	}
	if err := p.Cancel("unknown"); err == nil {
		t.Fatal("unknown job accepted")
	}
}
