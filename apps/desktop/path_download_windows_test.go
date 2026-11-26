package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	mv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

func TestDesktopVersionReviewBindsDeviceShareAndManifest(t *testing.T) {
	dir := t.TempDir()
	empty := sha256.Sum256(nil)
	m := mv1.Manifest{SchemaVersion: 1, ShareID: "chosen", Name: "空文件.txt", ContentID: hex.EncodeToString(empty[:]), ChunkSizeBytes: manifest.MinChunkSize, Chunks: []mv1.Chunk{}}
	m.ManifestID = manifest.ID(m)
	v := v1.PathVersion{ShareID: m.ShareID, Path: m.Name, Name: m.Name, ManifestID: m.ManifestID, ContentID: m.ContentID, PayloadKind: "file", PublishedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("unexpected control request", r.Method, r.URL.Path)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(v1.Capabilities{APIVersion: "v1", Operations: []string{"published_path_index"}, DataTransport: "external_sftp"})
		case "/v1/shares/chosen/index":
			_ = json.NewEncoder(w).Encode(v)
		case "/v1/shares/chosen/manifests/" + m.ManifestID:
			_ = json.NewEncoder(w).Encode(mv1.Published{Manifest: m, SFTPPath: "/synthetic"})
		default:
			t.Error("wrong share or version", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ca, token := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "client.token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dir, "state.db")
	manager := app.NewManager(database, nil)
	defer manager.Close(t.Context())
	d := Device{ID: "d", Name: "trusted", AgentURL: server.URL, CAFile: ca, TokenFile: token, ShareID: "wrong-default", SSHAddress: "127.0.0.1:1", SSHUser: "test", KeyFile: token, KnownHostsFile: ca}
	a := &App{ctx: t.Context(), manager: manager, database: database, devices: []Device{d}}
	review, err := a.InspectPublishedPath("d", "chosen", m.Name)
	if err != nil || review.Error != nil || review.ReviewID == "" {
		t.Fatal(review, err)
	}
	a.devices[0].Name = "edited"
	if _, err := a.StartPublishedDownload(review.ReviewID, filepath.Join(dir, "denied.txt")); err == nil {
		t.Fatal("edited device reused reviewed trust")
	}
	a.devices[0] = d
	review, err = a.InspectPublishedPath("d", "chosen", m.Name)
	if err != nil {
		t.Fatal(err)
	}
	expired := a.pathReviews[review.ReviewID]
	expired.expires = time.Now().Add(-time.Second)
	a.pathReviews[review.ReviewID] = expired
	if _, err := a.StartPublishedDownload(review.ReviewID, filepath.Join(dir, "expired.txt")); err == nil {
		t.Fatal("expired review accepted")
	}
	review, err = a.InspectPublishedPath("d", "chosen", m.Name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartPublishedDownload(review.ReviewID, filepath.Join(dir, "saved.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = manager.Wait(t.Context(), id) // Empty snapshot exercises the persisted task binding without file bytes.
	store, err := sqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task, err := store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var source app.DownloadRequest
	if err := json.Unmarshal(task.Source, &source); err != nil {
		t.Fatal(err)
	}
	if source.ShareID != "chosen" || source.ManifestID != m.ManifestID {
		t.Fatal("review fell back to default device share", source.ShareID, source.ManifestID)
	}
	if _, err := a.StartPublishedDownload(review.ReviewID, filepath.Join(dir, "duplicate.txt")); err == nil {
		t.Fatal("consumed review created duplicate task")
	}
}
