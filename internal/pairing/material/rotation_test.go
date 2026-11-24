package material

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
)

func replacement(t *testing.T, original materialFixture) materialFixture {
	t.Helper()
	f := fixture(t)
	f.store, f.request = original.store, original.request
	f.device.RequestID = original.device.RequestID
	f.expected.RequestID = original.expected.RequestID
	f.files["client.token"] = []byte(strings.Repeat("c", 64))
	var err error
	f.files["device.json"], err = json.Marshal(f.device)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTrustRotationKeepsOldPathsAndPublishesImmutableVersions(t *testing.T) {
	old := fixture(t)
	before, err := ImportReply(t.Context(), old.store, old.data(t), old.expected)
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string][]byte)
	for _, name := range []string{"ca.crt", "client.token", "known_hosts", "connection.json", "device.json"} {
		original[name], err = os.ReadFile(filepath.Join(filepath.Dir(before.ConnectionFile), name))
		if err != nil {
			t.Fatal(err)
		}
	}
	next := replacement(t, old)
	_, err = ImportReply(t.Context(), next.store, next.data(t), next.expected)
	assertCode(t, err, "trust_conflict")
	result, err := StageTrustRotation(t.Context(), next.store, next.data(t), next.expected)
	if err != nil || result.ConnectionFile == before.ConnectionFile || result.Verification != "not_run" {
		t.Fatal(result, err)
	}
	var connection v1.Connection
	data, err := os.ReadFile(result.ConnectionFile)
	if err != nil || json.Unmarshal(data, &connection) != nil {
		t.Fatal(err)
	}
	if connection.KeyFile != filepath.Join(old.store, old.expected.RequestID, "id_ed25519") || filepath.Dir(connection.CAFile) != filepath.Dir(result.ConnectionFile) {
		t.Fatal("incorrect versioned references")
	}
	for name, expected := range original {
		actual, err := os.ReadFile(filepath.Join(filepath.Dir(before.ConnectionFile), name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("old task credential path changed", name, err)
		}
	}
	// Formatting is not a different trust version.
	next.files["device.json"], err = json.MarshalIndent(next.device, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	again, err := StageTrustRotation(t.Context(), next.store, next.data(t), next.expected)
	if err != nil || again != result {
		t.Fatal("rotation retry was not idempotent", again, err)
	}
	if err := os.WriteFile(connection.TokenFile, []byte(strings.Repeat("d", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = StageTrustRotation(t.Context(), next.store, next.data(t), next.expected)
	assertCode(t, err, "trust_conflict")
}

func TestRotationRejectsMissingOriginalWrongPinsCancellationAndBusyStore(t *testing.T) {
	f := fixture(t)
	_, err := StageTrustRotation(t.Context(), f.store, f.data(t), f.expected)
	assertCode(t, err, "trust_not_found")
	if _, err := ImportReply(t.Context(), f.store, f.data(t), f.expected); err != nil {
		t.Fatal(err)
	}
	next := replacement(t, f)
	wrong := next.expected
	wrong.SSHFingerprint = f.expected.SSHFingerprint
	_, err = StageTrustRotation(t.Context(), next.store, next.data(t), wrong)
	assertCode(t, err, "ssh_fingerprint_mismatch")
	wrong = next.expected
	wrong.RequestID = ""
	_, err = StageTrustRotation(t.Context(), next.store, next.data(t), wrong)
	assertCode(t, err, "binding_mismatch")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := StageTrustRotation(ctx, next.store, next.data(t), next.expected); err != context.Canceled {
		t.Fatal(err)
	}
	lock, err := filesystem.Lock(filepath.Join(f.store, f.expected.RequestID, ".import.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = StageTrustRotation(t.Context(), next.store, next.data(t), next.expected)
	_ = lock.Close()
	assertCode(t, err, "import_busy")
	entries, err := os.ReadDir(filepath.Join(f.store, f.expected.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "paired-") || strings.HasPrefix(entry.Name(), ".import-") {
			t.Fatal("failed rotation published a directory", entry.Name())
		}
	}
}
