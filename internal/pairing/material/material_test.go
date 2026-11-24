package material

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/security"
	"golang.org/x/crypto/ssh"
)

type materialFixture struct {
	store    string
	request  RequestResult
	device   v1.ReplyDevice
	files    map[string][]byte
	expected Expected
}

func fixture(t *testing.T) materialFixture {
	t.Helper()
	store := filepath.Join(t.TempDir(), "trust")
	request, err := CreateRequest(t.Context(), RequestOptions{TrustDirectory: store, SourceIP: "100.64.0.1", Name: "发送设备"})
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := security.NewTLSBundle([]string{"100.64.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	expected := Expected{RequestID: request.Request.RequestID, ReceiverIP: "100.64.0.2", SSHFingerprint: ssh.FingerprintSHA256(host), CASHA256: digest(bundle.CA)}
	device := v1.ReplyDevice{SchemaVersion: 1, Kind: "hopsend-pair-reply", RequestID: expected.RequestID, Name: "接收电脑", AgentURL: "https://100.64.0.2:9443", SSHAddress: "100.64.0.2:22", SSHUser: "hopsend", HostPublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host))), HostFingerprint: expected.SSHFingerprint, CASHA256: expected.CASHA256, ShareID: "files", RemoteAgentConfig: `C:/HopSend/trust/node/identity/agent.local.json`}
	deviceData, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return materialFixture{store: store, request: request, device: device, expected: expected, files: map[string][]byte{"ca.crt": bundle.CA, "client.token": []byte(strings.Repeat("b", 64) + "\n"), "device.json": deviceData}}
}

type zipItem struct {
	name string
	data []byte
	mode os.FileMode
}

func archive(t *testing.T, items []zipItem) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, item := range items {
		h := &zip.FileHeader{Name: item.name, Method: zip.Store}
		if item.mode != 0 {
			h.SetMode(item.mode)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func (f materialFixture) data(t *testing.T) []byte {
	t.Helper()
	return archive(t, []zipItem{{"ca.crt", f.files["ca.crt"], 0}, {"client.token", f.files["client.token"], 0}, {"device.json", f.files["device.json"], 0}})
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var detail *Error
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("want code %q, got %v", code, err)
	}
}

func TestRequestAndReplyImportWithRetry(t *testing.T) {
	f := fixture(t)
	keyData, err := os.ReadFile(filepath.Join(f.store, f.expected.RequestID, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != f.request.Request.PublicKey {
		t.Fatalf("generated key does not match public request: %v", err)
	}
	if !bytes.HasPrefix(keyData, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		t.Fatal("key is not OpenSSH format")
	}
	result, err := ImportReply(t.Context(), f.store, f.data(t), f.expected)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verification != "not_run" {
		t.Fatal("local import claimed network verification")
	}
	configData, err := os.ReadFile(result.ConnectionFile)
	if err != nil || bytes.Contains(configData, f.files["client.token"][:64]) || bytes.Contains(configData, keyData) {
		t.Fatal("configuration leaked key or Token", err)
	}
	var config v1.Connection
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatal(err)
	}
	if config.KeyFile != filepath.Join(f.store, f.expected.RequestID, "id_ed25519") || !config.ManageAgent || config.AgentURL != f.device.AgentURL {
		t.Fatal("wrong connection references")
	}
	firstKnown, err := os.ReadFile(config.KnownHostsFile)
	if err != nil || string(firstKnown) != f.expected.ReceiverIP+" "+f.device.HostPublicKey+"\n" {
		t.Fatal("wrong pinned host record", err)
	}
	// Windows PowerShell writes differently formatted JSON. A retry compares
	// derived connection semantics while the three received materials stay exact.
	compact, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.ConnectionFile, compact, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := ImportReply(t.Context(), f.store, f.data(t), f.expected)
	if err != nil || again != result {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	after, err := os.ReadFile(result.ConnectionFile)
	if err != nil || !bytes.Equal(after, compact) {
		t.Fatal("retry rewrote fixed trust", err)
	}
}

func TestImportRejectsChangedTrustAndKey(t *testing.T) {
	t.Run("changed Token", func(t *testing.T) {
		f := fixture(t)
		result, err := ImportReply(t.Context(), f.store, f.data(t), f.expected)
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(result.ConnectionFile)
		if err != nil {
			t.Fatal(err)
		}
		f.files["client.token"] = []byte(strings.Repeat("c", 64))
		_, err = ImportReply(t.Context(), f.store, f.data(t), f.expected)
		assertCode(t, err, "trust_conflict")
		after, err := os.ReadFile(result.ConnectionFile)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("changed trust corrupted original", err)
		}
		actual, err := os.ReadFile(filepath.Join(filepath.Dir(result.ConnectionFile), "client.token"))
		if err != nil || string(actual) != strings.Repeat("b", 64)+"\n" {
			t.Fatal("Token was overwritten", err)
		}
	})
	t.Run("mismatched private key", func(t *testing.T) {
		f := fixture(t)
		other := fixture(t)
		key, err := os.ReadFile(filepath.Join(other.store, other.expected.RequestID, "id_ed25519"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.store, f.expected.RequestID, "id_ed25519"), key, 0600); err != nil {
			t.Fatal(err)
		}
		_, err = ImportReply(t.Context(), f.store, f.data(t), f.expected)
		assertCode(t, err, "private_key_mismatch")
		if _, err := os.Stat(filepath.Join(f.store, f.expected.RequestID, "paired")); !os.IsNotExist(err) {
			t.Fatal("unverified trust was published")
		}
	})
}

func TestImportLockAndCancellation(t *testing.T) {
	f := fixture(t)
	lock, err := filesystem.Lock(filepath.Join(f.store, f.expected.RequestID, ".import.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportReply(t.Context(), f.store, f.data(t), f.expected)
	assertCode(t, err, "import_busy")
	lock.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ImportReply(ctx, f.store, f.data(t), f.expected)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.store, f.expected.RequestID, "paired")); !os.IsNotExist(err) {
		t.Fatal("cancelled import published trust")
	}
	if _, err := ImportReply(t.Context(), f.store, f.data(t), f.expected); err != nil {
		t.Fatal("lock was not released", err)
	}
}

func TestReplyRejectsInvalidMaterialsBeforeWriting(t *testing.T) {
	for _, kind := range []string{"ssh_pin", "ca_pin", "request_binding", "receiver_binding", "extra", "duplicate", "traversal", "symlink", "large_entry", "large_archive", "crc", "invalid_ca", "invalid_token", "duplicate_json", "unknown_json", "extra_json", "invalid_path", "mismatched_address"} {
		t.Run(kind, func(t *testing.T) {
			f := fixture(t)
			expected := f.expected
			items := []zipItem{{"ca.crt", f.files["ca.crt"], 0}, {"client.token", f.files["client.token"], 0}, {"device.json", f.files["device.json"], 0}}
			data := []byte(nil)
			switch kind {
			case "ssh_pin":
				expected.SSHFingerprint = "SHA256:" + strings.Repeat("A", 43)
			case "ca_pin":
				expected.CASHA256 = strings.Repeat("0", 64)
			case "request_binding":
				expected.RequestID = strings.Repeat("a", 32)
			case "receiver_binding":
				expected.ReceiverIP = "100.64.0.3"
			case "extra":
				items = append(items, zipItem{"id_ed25519", []byte("must not exchange private keys"), 0})
			case "duplicate":
				items[1].name = "ca.crt"
			case "traversal":
				items[0].name = "../ca.crt"
			case "symlink":
				items[0].mode = os.ModeSymlink | 0600
			case "large_entry":
				items[0].data = make([]byte, v1.MaxJSONBytes+1)
			case "large_archive":
				data = append(f.data(t), make([]byte, v1.MaxReplyBytes)...)
			case "crc":
				data = f.data(t)
				i := bytes.Index(data, f.files["client.token"])
				if i < 0 {
					t.Fatal("test ZIP payload missing")
				}
				data[i] = 'c'
			case "invalid_ca":
				items[0].data = []byte("pinned text is not a CA")
				expected.CASHA256 = digest(items[0].data)
				f.device.CASHA256 = expected.CASHA256
				items[2].data, _ = json.Marshal(f.device)
			case "invalid_token":
				items[1].data = []byte("invalid Token")
			case "duplicate_json":
				items[2].data = append([]byte(`{"request_id":"`+f.expected.RequestID+`",`), items[2].data[1:]...)
			case "unknown_json":
				items[2].data = append([]byte(`{"extra":true,`), items[2].data[1:]...)
			case "extra_json":
				items[2].data = append(items[2].data, []byte(` {}`)...)
			case "invalid_path":
				f.device.RemoteAgentConfig = "C:/HopSend/../outside.json"
				items[2].data, _ = json.Marshal(f.device)
			case "mismatched_address":
				f.device.AgentURL = "https://100.64.0.3:9443"
				items[2].data, _ = json.Marshal(f.device)
			}
			if data == nil {
				data = archive(t, items)
			}
			_, err := ImportReply(t.Context(), f.store, data, expected)
			if err == nil {
				t.Fatal("invalid materials accepted")
			}
			if strings.Contains(err.Error(), strings.Repeat("b", 64)) || strings.Contains(err.Error(), "BEGIN OPENSSH PRIVATE KEY") {
				t.Fatal("secret leaked in error")
			}
			if _, err := os.Stat(filepath.Join(f.store, f.expected.RequestID, "paired")); !os.IsNotExist(err) {
				t.Fatal("invalid materials wrote paired state")
			}
		})
	}
}

func TestRequestRejectsInvalidInputsWithoutSideEffects(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "100.63.255.255", "100.128.0.1", "100.064.0.1", "100.64.0.1/32", "https://100.64.0.1"} {
		store := filepath.Join(t.TempDir(), "not-created")
		if _, err := CreateRequest(t.Context(), RequestOptions{TrustDirectory: store, SourceIP: address}); err == nil {
			t.Fatal("invalid IP accepted", address)
		}
		if _, err := os.Stat(store); !os.IsNotExist(err) {
			t.Fatal("invalid IP created store")
		}
	}
}

func TestRequestRejectsAmbiguousJSON(t *testing.T) {
	f := fixture(t)
	data, err := json.Marshal(f.request.Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		append([]byte(`{"request_id":"`+f.expected.RequestID+`",`), data[1:]...),
		bytes.Replace(data, []byte(`"request_id"`), []byte(`"REQUEST_ID"`), 1),
		append(data, []byte(` {}`)...),
		append([]byte{0xff}, data...),
	} {
		if _, err := ParseRequest(invalid); err == nil {
			t.Fatal("ambiguous request JSON accepted")
		}
	}
}
