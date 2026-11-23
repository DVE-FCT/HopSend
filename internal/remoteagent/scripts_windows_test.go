//go:build windows

package remoteagent

import (
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
)

func execute(t *testing.T, script string, values any) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	isolated := make(map[string]string)
	for k, v := range values.(map[string]string) {
		isolated[k] = v
	}
	isolated["test_appdata"] = t.TempDir()
	// Keep the normal Windows PowerShell startup environment; isolate only the
	// preparation script's files after the process and its modules have loaded.
	command := powershell("$env:LOCALAPPDATA=$p.test_appdata;"+script, isolated)
	suffix := strings.Split(command, "-EncodedCommand ")[1]
	p := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", suffix)
	return p.CombinedOutput()
}

func TestPreparationPreservesQuotedConfigurationAndVerifiesBinary(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "quoted '$dollar ; 配对.json")
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := execute(t, environmentScript, map[string]string{"config": config})
	if err != nil {
		t.Fatal(err, string(output))
	}
	var value struct{ Config, Platform string }
	if json.Unmarshal(output, &value) != nil || value.Platform != "windows-x64" {
		t.Fatal(string(output))
	}
	expectedInfo, expectedErr := os.Stat(config)
	actualInfo, actualErr := os.Stat(value.Config)
	if expectedErr != nil || actualErr != nil || !os.SameFile(expectedInfo, actualInfo) {
		t.Fatalf("configuration reference changed: expected=%q actual=%q errors=%v/%v", config, value.Config, expectedErr, actualErr)
	}
	binary := filepath.Join(root, "program '$test.exe")
	if err = os.WriteFile(binary, []byte("binary fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("binary fixture"))
	output, err = execute(t, verifyScript, map[string]string{"executable": binary, "sha256": hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err, string(output))
	}
	if _, err = execute(t, verifyScript, map[string]string{"executable": binary, "sha256": strings.Repeat("0", 64)}); err == nil {
		t.Fatal("mismatched binary accepted")
	}
	if _, err = execute(t, environmentScript, map[string]string{"config": "relative/config.json"}); err == nil {
		t.Fatal("relative remote configuration accepted")
	}
	temporary := filepath.Join(root, "upload.tmp")
	published := filepath.Join(root, "published.exe")
	if err = os.WriteFile(temporary, []byte("binary fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := execute(t, publishScript, map[string]string{"temporary": temporary, "executable": published, "sha256": hex.EncodeToString(sum[:])}); err != nil {
		t.Fatal(err, string(out))
	}
	if actual, err := os.ReadFile(published); err != nil || string(actual) != "binary fixture" {
		t.Fatal("verified program not published", err)
	}
	if err = os.WriteFile(temporary, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = execute(t, publishScript, map[string]string{"temporary": temporary, "executable": filepath.Join(root, "bad.exe"), "sha256": hex.EncodeToString(sum[:])}); err == nil {
		t.Fatal("corrupt deployment published")
	}
}
