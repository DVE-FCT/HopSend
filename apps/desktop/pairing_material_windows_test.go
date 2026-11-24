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
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/security"
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
	t.Log("desktop create -> inspect without trust -> explicit matching pins -> local import/save -> retry -> changed file rejection passed; live preparation remains separate")
}
