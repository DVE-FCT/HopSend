//go:build windows

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/pairing/v1"
	"golang.org/x/crypto/ssh"
)

func TestPairMaterialsGoCLIAndLegacyPowerShell(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "资料 ' literal")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "hopsend-cli.exe")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./apps/cli")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v %s", err, output)
	}
	run := func(args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("compiled CLI %s failed: %v %s", args[0], err, output)
		}
		return output
	}
	trust := filepath.Join(dir, "trust")
	output := run("pair", "request", "--trust-dir", trust, "--out-dir", dir, "--source-ip", "100.64.0.1", "--name", "literal'; throw 'must stay data")
	var request struct {
		Request v1.Request `json:"request"`
		File    string     `json:"request_file"`
	}
	if err := json.Unmarshal(output, &request); err != nil || request.Request.RequestID == "" {
		t.Fatalf("invalid request result: %v", err)
	}
	privatePath := filepath.Join(trust, request.Request.RequestID, "id_ed25519")
	keygen := filepath.Join(os.Getenv("WINDIR"), "System32", "OpenSSH", "ssh-keygen.exe")
	derived, err := exec.CommandContext(ctx, keygen, "-y", "-P", "", "-f", privatePath).CombinedOutput()
	if err != nil {
		t.Fatalf("Windows OpenSSH rejected Go-generated private key: %v %s", err, derived)
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey(derived)
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))) != request.Request.PublicKey {
		t.Fatalf("OpenSSH public key mismatch: %v", err)
	}
	fingerprint := ssh.FingerprintSHA256(public)
	identity := filepath.Join(dir, "identity")
	run("agent-init", "--dir", identity)
	caPath := filepath.Join(identity, "ca.crt")
	ca, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	caSum := sha256.Sum256(ca)
	caHash := hex.EncodeToString(caSum[:])
	token, err := os.ReadFile(filepath.Join(identity, "client.token"))
	if err != nil {
		t.Fatal(err)
	}
	replyDir := filepath.Join(dir, "reply")
	if err := os.Mkdir(replyDir, 0700); err != nil {
		t.Fatal(err)
	}
	replyPath := filepath.Join(dir, "legacy-reply.zip")
	script := filepath.Join(dir, "legacy-reply.ps1")
	// Produce the response with actual Windows PowerShell 5.1 JSON/ZIP APIs,
	// using the legacy wire fields. No production pairing scripts are needed.
	code := `param([string]$RequestFile,[string]$Identity,[string]$Output,[string]$Archive,[string]$Fingerprint)
$ErrorActionPreference='Stop'
$request=[IO.File]::ReadAllText($RequestFile)|ConvertFrom-Json
Copy-Item -LiteralPath (Join-Path $Identity 'ca.crt'),(Join-Path $Identity 'client.token') -Destination $Output
$device=[ordered]@{schema_version=1;kind='hopsend-pair-reply';request_id=$request.request_id;name='Legacy receiver';agent_url='https://100.64.0.2:9443';ssh_address='100.64.0.2:22';ssh_user='hopsend';host_public_key=$request.public_key;host_fingerprint=$Fingerprint;ca_sha256=(Get-FileHash -LiteralPath (Join-Path $Identity 'ca.crt') -Algorithm SHA256).Hash.ToLowerInvariant();share_id='files';remote_agent_config='C:/HopSend/identity/agent.local.json'}
[IO.File]::WriteAllText((Join-Path $Output 'device.json'),($device|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
Compress-Archive -LiteralPath (Join-Path $Output 'ca.crt'),(Join-Path $Output 'client.token'),(Join-Path $Output 'device.json') -DestinationPath $Archive
`
	if err := os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	ps := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-RequestFile", request.File, "-Identity", identity, "-Output", replyDir, "-Archive", replyPath, "-Fingerprint", fingerprint)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") {
			ps.Env = append(ps.Env, entry)
		}
	}
	ps.Env = append(ps.Env, "PSModulePath="+filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "Modules"))
	if output, err := ps.CombinedOutput(); err != nil {
		t.Fatalf("legacy PowerShell producer failed: %v %s", err, output)
	}
	shared := []string{"--reply", replyPath, "--expected-ssh", fingerprint, "--expected-ca", caHash, "--request-id", request.Request.RequestID, "--receiver-ip", "100.64.0.2"}
	inspected := run(append([]string{"pair", "inspect"}, shared...)...)
	if bytes.Contains(inspected, bytes.TrimSpace(token)) || bytes.Contains(inspected, []byte("PRIVATE KEY")) {
		t.Fatal("inspection leaked a secret")
	}
	var inspection struct {
		Verified bool           `json:"materials_verified"`
		Device   v1.ReplyDevice `json:"device"`
	}
	if err := json.Unmarshal(inspected, &inspection); err != nil || !inspection.Verified || inspection.Device.RequestID != request.Request.RequestID {
		t.Fatal("legacy reply was not validated", err)
	}
	args := append([]string{"pair", "import", "--trust-dir", trust}, shared...)
	first := run(args...)
	second := run(args...)
	if !bytes.Equal(first, second) || bytes.Contains(first, bytes.TrimSpace(token)) {
		t.Fatal("CLI import retry changed result or exposed credentials")
	}
	var imported struct {
		File         string `json:"connection_file"`
		Verification string `json:"verification"`
	}
	if err := json.Unmarshal(first, &imported); err != nil || imported.Verification != "not_run" {
		t.Fatal("local import reported network verification", err)
	}
	config, err := os.ReadFile(imported.File)
	if err != nil || bytes.Contains(config, bytes.TrimSpace(token)) || bytes.Contains(config, []byte("PRIVATE KEY")) {
		t.Fatal("connection configuration embeds credentials", err)
	}
	bad := exec.CommandContext(ctx, binary, "pair", "inspect", "--reply", replyPath, "--expected-ssh", fingerprint, "--expected-ca", strings.Repeat("0", 64))
	failed, err := bad.CombinedOutput()
	if err == nil || bytes.Contains(failed, bytes.TrimSpace(token)) {
		t.Fatal("bad pin accepted or error leaked credentials")
	}
	var detail struct {
		Error struct {
			Code  string `json:"code"`
			Stage string `json:"stage"`
		} `json:"error"`
	}
	if err := json.Unmarshal(failed, &detail); err != nil || detail.Error.Code != "ca_fingerprint_mismatch" || detail.Error.Stage == "" {
		t.Fatalf("CLI error is not structured: %v %s", err, failed)
	}
	t.Log("compiled Go CLI request -> Windows OpenSSH key verification -> WinPS5.1 legacy ZIP -> Go inspect/import/retry passed; no network or elevation")
}
