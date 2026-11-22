package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publishSample(t *testing.T, data []byte) (string, string, v1.Manifest) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "中文文件.bin")
	store := filepath.Join(dir, "content")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Publish(context.Background(), source, store, "files", MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", m.ContentID), 0600) })
	return source, store, m
}

func TestPublishedSnapshotRemainsIndependentAndIdempotent(t *testing.T) {
	data := bytes.Repeat([]byte("content"), 30000)
	source, store, m := publishSample(t, data)
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	again, err := Publish(context.Background(), source, store, "files", MinChunkSize)
	if err != nil || again.ManifestID != m.ManifestID {
		t.Fatal(again, err)
	}
	if err := os.WriteFile(source, []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(filepath.Join(store, "objects", m.ContentID))
	if err != nil || !bytes.Equal(snapshot, data) {
		t.Fatal("snapshot changed with source", err)
	}
	root, err := os.OpenRoot(store)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	loaded, err := Load(root, m.ManifestID)
	if err != nil || loaded.ContentID != m.ContentID {
		t.Fatal(loaded, err)
	}
}

func TestEmptyAndPartialFinalChunk(t *testing.T) {
	for _, size := range []int{0, 1, int(MinChunkSize), int(MinChunkSize) + 17} {
		_, _, m := publishSample(t, bytes.Repeat([]byte{9}, size))
		expected := sha256.Sum256(bytes.Repeat([]byte{9}, size))
		if m.SizeBytes != int64(size) || m.ContentID != hex.EncodeToString(expected[:]) {
			t.Fatal(m)
		}
		if err := Validate(m); err != nil {
			t.Fatal(size, err)
		}
	}
}

func TestRejectsMutatedManifestAndCorruptStore(t *testing.T) {
	source, store, m := publishSample(t, bytes.Repeat([]byte{3}, 100000))
	encoded, _ := json.Marshal(m)
	for _, mutate := range []func(*v1.Manifest){
		func(m *v1.Manifest) { m.Chunks[0].Offset = 1 },
		func(m *v1.Manifest) { m.Chunks[0].Length = 1<<63 - 1 },
		func(m *v1.Manifest) { m.Name = "../escape" },
		func(m *v1.Manifest) { m.Name = "CON.txt" },
		func(m *v1.Manifest) { m.SizeBytes = -1 },
		func(m *v1.Manifest) { m.ContentID = strings.Repeat("f", 64) },
	} {
		var copy v1.Manifest
		if err := json.Unmarshal(encoded, &copy); err != nil {
			t.Fatal(err)
		}
		mutate(&copy)
		if err := Validate(copy); err == nil {
			t.Fatal("mutated manifest accepted")
		}
	}
	object := filepath.Join(store, "objects", m.ContentID)
	if err := os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(context.Background(), source, store, "files", MinChunkSize); err == nil {
		t.Fatal("corrupt existing object reused")
	}
}

func TestDecodeRejectsUnknownFieldsAndExtraJSON(t *testing.T) {
	_, _, m := publishSample(t, []byte("sample"))
	data, _ := json.Marshal(m)
	if _, err := Decode(bytes.NewReader(append(data, []byte(" {}")...))); err == nil {
		t.Fatal("extra JSON accepted")
	}
	bad := append([]byte(`{"unknown":true,`), data[1:]...)
	if _, err := Decode(bytes.NewReader(bad)); err == nil {
		t.Fatal("unknown fields accepted")
	}
}

func TestValidateUses64BitOffsetsBeyondFourGiB(t *testing.T) {
	_, _, m := publishSample(t, []byte("seed"))
	m.ChunkSizeBytes = MaxChunkSize
	m.SizeBytes = (int64(1) << 32) + 17
	m.Chunks = nil
	for offset := int64(0); offset < m.SizeBytes; offset += MaxChunkSize {
		m.Chunks = append(m.Chunks, v1.Chunk{Index: len(m.Chunks), Offset: offset, Length: min(MaxChunkSize, m.SizeBytes-offset), SHA256: m.ContentID})
	}
	m.ManifestID = ID(m)
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
	if m.Chunks[len(m.Chunks)-1].Offset != int64(1)<<32 {
		t.Fatal("offset truncated")
	}
}
