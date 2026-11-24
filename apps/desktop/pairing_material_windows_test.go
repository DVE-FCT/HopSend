//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/security"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	"golang.org/x/crypto/ssh"
)

func TestDesktopPairingBridgeInspectsPinsImportsAndSaves(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	directory := filepath.Join(local, "desktop")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manager := app.NewManager(filepath.Join(directory, "state.db"), nil)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	a := &App{ctx: t.Context(), directory: directory, manager: manager, pairReadLocal: func(context.Context) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"TailscaleIPs":["100.64.0.1"]}}`), nil
	}}
	if _, err := a.PairCreateRequest("100.64.0.3", "wrong"); err == nil {
		t.Fatal("unconfirmed local IP accepted")
	}
	request, err := a.PairCreateRequest("100.64.0.1", "客户端")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request.RequestJSON, "PRIVATE KEY") || request.Request.RequestID == "" {
		t.Fatal("request is not public")
	}
	bundle, err := security.NewTLSBundle([]string{"100.64.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	caSum := sha256.Sum256(bundle.CA)
	caHash := hex.EncodeToString(caSum[:])
	fingerprint := ssh.FingerprintSHA256(host)
	device := v1.ReplyDevice{SchemaVersion: 1, Kind: "hopsend-pair-reply", RequestID: request.Request.RequestID, Name: "可信源设备", AgentURL: "https://100.64.0.2:9443", SSHAddress: "100.64.0.2:22", SSHUser: "hopsend", HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))), HostFingerprint: fingerprint, CASHA256: caHash, ShareID: "files", RemoteAgentConfig: "C:/HopSend/identity/agent.local.json"}
	deviceJSON, err := json.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, data := range map[string][]byte{"ca.crt": bundle.CA, "client.token": []byte(strings.Repeat("b", 64)), "device.json": deviceJSON} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(local, "reply ' literal.hopsend-pair")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PairImportReply(path, fingerprint, caHash); err == nil {
		t.Fatal("import skipped preview")
	}
	preview, err := a.PairInspectReply(path)
	if err != nil || !preview.MaterialsVerified || preview.TrustVerified {
		t.Fatal(preview, err)
	}
	paired := filepath.Join(local, "HopSend", "trust", request.Request.RequestID, "paired")
	if _, err := os.Stat(paired); !os.IsNotExist(err) {
		t.Fatal("inspection persisted trust")
	}
	if _, err := a.PairImportReply(path, "SHA256:"+strings.Repeat("A", 43), caHash); err == nil {
		t.Fatal("wrong trusted pin accepted")
	}
	if devices, _ := a.Devices(); len(devices) != 0 {
		t.Fatal("wrong pin saved a device")
	}
	for range 2 {
		result, err := a.PairImportReply(path, fingerprint, strings.ToUpper(caHash))
		if err != nil || result != filepath.Join(paired, "connection.json") {
			t.Fatal(result, err)
		}
	}
	devices, err := a.Devices()
	if err != nil || len(devices) != 1 || devices[0].ID != request.Request.RequestID || devices[0].SSHUser != "hopsend" || !devices[0].ManageAgent {
		t.Fatal("device was not automatically populated and saved", err)
	}
	if err := os.WriteFile(path, append(buffer.Bytes(), []byte("changed after review")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PairImportReply(path, fingerprint, caHash); err == nil {
		t.Fatal("changed package accepted without a new review")
	}
	testDesktopRotation(t, a, device, devices[0])
	t.Log("desktop create -> inspect -> trusted import/save -> changed file rejection -> explicit versioned rotation and failed snapshot recovery passed")
}

func testDesktopRotation(t *testing.T, a *App, reply v1.ReplyDevice, original Device) {
	t.Helper()
	original.ManageAgent = false
	original.Name = "保留用户设备名"
	if _, err := a.SaveDevice(original); err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := os.ReadFile(filepath.Join(a.directory, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldCA, err := os.ReadFile(original.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(filepath.Join(a.directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := filepath.Join(a.directory, "old-task.bin")
	if err := os.WriteFile(source, []byte("persistent task fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Publish(t.Context(), source, filepath.Join(a.directory, "snapshot"), "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(a.directory, "download.bin")
	request := original.request()
	request.ManifestID, request.Destination = m.ManifestID, destination
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	task := engine.Task{ID: engine.TaskID(m.ManifestID, destination), Manifest: m, Destination: destination, Staging: destination + ".part", RemotePath: "old-task.bin", Source: requestData}
	if _, err := store.Prepare(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.SetState(t.Context(), task.ID, "paused"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := security.NewTLSBundle([]string{"100.64.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	reply.HostPublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host)))
	reply.HostFingerprint = ssh.FingerprintSHA256(host)
	caSum := sha256.Sum256(bundle.CA)
	reply.CASHA256 = hex.EncodeToString(caSum[:])
	replyJSON, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, data := range map[string][]byte{"ca.crt": bundle.CA, "client.token": []byte(strings.Repeat("c", 64)), "device.json": replyJSON} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.directory, "new-trust.hopsend-pair")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RotateDeviceTrust(original.ID, path, reply.HostFingerprint, reply.CASHA256); err == nil {
		t.Fatal("rotation skipped review")
	}
	if _, err := a.PairInspectReply(path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PairImportReply(path, reply.HostFingerprint, reply.CASHA256); err == nil {
		t.Fatal("ordinary import replaced fixed trust")
	}
	if _, err := a.RotateDeviceTrust("wrong-device", path, reply.HostFingerprint, reply.CASHA256); err == nil {
		t.Fatal("rotated a different device")
	}
	if _, err := a.RotateDeviceTrust(original.ID, path, "SHA256:"+strings.Repeat("A", 43), reply.CASHA256); err == nil {
		t.Fatal("wrong trusted pin rotated device")
	}

	// An exclusive Windows handle forces atomic snapshot replacement to fail.
	lock, err := filesystem.Lock(filepath.Join(a.directory, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, rotateErr := a.RotateDeviceTrust(original.ID, path, reply.HostFingerprint, reply.CASHA256)
	_ = lock.Close()
	if rotateErr == nil {
		t.Fatal("snapshot failure did not stop rotation")
	}
	unchanged, err := a.findDevice(original.ID)
	if err != nil || unchanged != original {
		t.Fatal("failed rotation changed in-memory device", err)
	}
	snapshot, err := os.ReadFile(filepath.Join(a.directory, "devices.json"))
	if err != nil || !bytes.Equal(snapshot, oldSnapshot) {
		t.Fatal("failed rotation changed disk snapshot", err)
	}

	rotated, err := a.RotateDeviceTrust(original.ID, path, reply.HostFingerprint, reply.CASHA256)
	if err != nil || rotated.ID != original.ID || rotated.KeyFile != original.KeyFile || rotated.CAFile == original.CAFile || rotated.Name != original.Name || rotated.ManageAgent {
		t.Fatal("rotation changed stable identity or settings", rotated, err)
	}
	again, err := a.RotateDeviceTrust(original.ID, path, reply.HostFingerprint, reply.CASHA256)
	if err != nil || again != rotated {
		t.Fatal("rotation retry changed device", err)
	}
	if _, err := a.PairImportReply(path, reply.HostFingerprint, reply.CASHA256); err == nil {
		t.Fatal("ordinary import reset rotated references")
	}
	actualCA, err := os.ReadFile(original.CAFile)
	if err != nil || !bytes.Equal(actualCA, oldCA) {
		t.Fatal("old task credentials changed", err)
	}
	after, err := store.Get(t.Context(), task.ID)
	if err != nil || after.ID != before.ID || after.State != before.State || !bytes.Equal(after.Source, before.Source) {
		t.Fatal("rotation rewrote task history or credential references", err)
	}
	devices, err := a.Devices()
	if err != nil || len(devices) != 1 {
		t.Fatal("rotation duplicated device", err)
	}

	stale := original
	if _, err := a.saveDevice(stale, &original); err == nil {
		t.Fatal("stale concurrent edit overwrote rotated trust")
	}
}

func TestManualPairAddressRequiresLocalTailscaleAdapter(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	manager := app.NewManager(filepath.Join(local, "state.db"), nil)
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	a := &App{ctx: t.Context(), directory: local, manager: manager,
		pairReadLocal:      func(context.Context) ([]byte, error) { return nil, errors.New("CLI unavailable") },
		pairLocalAddresses: func() ([]string, error) { return []string{"100.64.0.1"}, nil },
	}
	for _, address := range []string{"100.64.0.2", "127.0.0.1", "100.064.0.1", "fd7a:115c:a1e0::1", ""} {
		if _, err := a.PairCreateRequest(address, "客户端"); err == nil {
			t.Fatal("claimed foreign or noncanonical address", address)
		}
	}
	if _, err := os.Stat(filepath.Join(local, "HopSend", "trust")); !os.IsNotExist(err) {
		t.Fatal("invalid fallback created key material")
	}
	result, err := a.PairCreateRequest("100.64.0.1", "客户端")
	if err != nil || result.Request.SourceIP != "100.64.0.1" {
		t.Fatal(result, err)
	}
	a.pairReadLocal = func(context.Context) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.2"]}}`), nil
	}
	if err := a.verifyPairSource("100.64.0.1"); err == nil {
		t.Fatal("manual fallback overrode a readable authoritative status")
	}
	if err := a.verifyPairSource("100.64.0.2"); err != nil {
		t.Fatal(err)
	}
}
