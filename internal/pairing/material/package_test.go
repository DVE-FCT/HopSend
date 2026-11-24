package material

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPackagePreviewDoesNotEstablishTrust(t *testing.T) {
	f := fixture(t)
	preview, err := PreviewPackage(f.data(t))
	if err != nil || preview.TrustVerified || preview.Device.RequestID != f.expected.RequestID {
		t.Fatal(preview, err)
	}
	if _, err := LocalRequest(f.store, preview.Device.RequestID, f.expected.ReceiverIP); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.store, f.expected.RequestID, "paired")); !os.IsNotExist(err) {
		t.Fatal("preview published trust")
	}
	wrong := f.expected
	wrong.SSHFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := ImportReply(t.Context(), f.store, f.data(t), wrong); err == nil {
		t.Fatal("self-consistent preview bypassed independent pin check")
	}
}

func TestExportPairPackageIsPrivateBoundedAndNoReplace(t *testing.T) {
	f := fixture(t)
	destination := filepath.Join(t.TempDir(), "source.hopsend-pair")
	input := append(f.data(t), []byte("discard arbitrary trailing payload")...)
	preview, err := ExportPackage(t.Context(), input, f.expected, destination)
	if err != nil || preview.TrustVerified {
		t.Fatal(preview, err)
	}
	data, err := ReadPackage(destination)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("discard arbitrary trailing payload")) {
		t.Fatal("export retained unrelated payload")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) != 3 {
		t.Fatal("wrong exported archive", err)
	}
	if _, err := VerifyReply(data, f.expected); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportReply(t.Context(), f.store, data, f.expected); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportPackage(t.Context(), data, f.expected, destination); err == nil {
		t.Fatal("export overwrote existing package")
	}
	after, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("failed export corrupted existing package", err)
	}
	files, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(files) != 1 {
		t.Fatal("export left staging material", err)
	}
}

func TestPackagePreviewRejectsInvalidBindingAndShape(t *testing.T) {
	f := fixture(t)
	for _, data := range [][]byte{[]byte("not ZIP"), append(f.data(t), make([]byte, 256<<10)...), archive(t, []zipItem{{"device.json", f.files["device.json"], 0}})} {
		if _, err := PreviewPackage(data); err == nil {
			t.Fatal("invalid preview accepted")
		}
	}
	if _, err := LocalRequest(t.TempDir(), f.expected.RequestID, f.expected.ReceiverIP); err == nil {
		t.Fatal("foreign computer request accepted")
	}
}

func TestExportOriginalPublicRequestAndSemanticReplyRetry(t *testing.T) {
	f := fixture(t)
	destination := filepath.Join(t.TempDir(), "request.json")
	for range 2 {
		result, err := ExportRequest(f.store, f.expected.RequestID, f.expected.ReceiverIP, destination)
		if err != nil || result.Request != f.request.Request {
			t.Fatal(result, err)
		}
	}
	if _, err := ImportReply(t.Context(), f.store, f.data(t), f.expected); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, f.files["device.json"]); err != nil {
		t.Fatal(err)
	}
	f.files["device.json"] = compact.Bytes()
	if _, err := ImportReply(t.Context(), f.store, f.data(t), f.expected); err != nil {
		t.Fatal("equivalent source response rejected", err)
	}
}
