package folder

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryBundleKeepsEmptyDirectoriesChineseLongPathsAndBytes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "文件夹")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{"空目录", "中文/深层/空目录", strings.Repeat("长目录", 8) + "/" + strings.Repeat("较长路径", 8) + "/" + strings.Repeat("第三层", 8)}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Join(source, filepath.FromSlash(path)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"空文件.txt": nil, "中文/深层/样例.bin": bytes.Repeat([]byte("中文"), 40000), paths[2] + "/末尾.txt": []byte("long path data")}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(path)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "folder.tar")
	if _, err := Create(context.Background(), source, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "received")
	if err := Publish(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		actual, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal(path, err)
		}
	}
	for _, path := range paths {
		info, err := os.Stat(filepath.Join(target, filepath.FromSlash(path)))
		if err != nil || !info.IsDir() {
			t.Fatal(path, err)
		}
	}
	if err := Publish(context.Background(), archive, target); err == nil {
		t.Fatal("existing directory overwritten")
	}
}

func TestDirectoryRejectsTraversalLinksReservedNamesDuplicatesAndWrongHashes(t *testing.T) {
	for _, path := range []string{"../escape", "/absolute", "C:/drive", "a/../../bad", "a\\evil", "CON.txt", "a/NUL", "a/name.", "a/name ", "x:stream", indexName} {
		if SafePath(path) == nil {
			t.Errorf("unsafe path accepted %q", path)
		}
	}
	for _, kind := range []string{"traversal", "symlink", "hardlink", "duplicate", "case_collision", "bad_hash", "missing_index", "huge_index"} {
		t.Run(kind, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.tar")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			w := tar.NewWriter(f)
			h := &tar.Header{Name: "file.bin", Size: 1, Mode: 0600, Typeflag: tar.TypeReg}
			switch kind {
			case "traversal":
				h.Name = "../escaped"
			case "symlink":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "outside"
				h.Size = 0
			case "hardlink":
				h.Typeflag = tar.TypeLink
				h.Linkname = "outside"
				h.Size = 0
			}
			if err = w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Size > 0 {
				_, _ = w.Write([]byte("x"))
			}
			if kind == "duplicate" || kind == "case_collision" {
				if kind == "case_collision" {
					h.Name = "FILE.bin"
				}
				if err = w.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				_, _ = w.Write([]byte("x"))
			}
			if kind != "missing_index" {
				index := Index{Version: 1, Name: "folder", Entries: []Entry{{Path: "file.bin", Bytes: 1, SHA256: strings.Repeat("a", 64)}}}
				b, _ := json.Marshal(index)
				size := int64(len(b))
				if kind == "huge_index" {
					size = maxIndexBytes + 1
				}
				if err = w.WriteHeader(&tar.Header{Name: indexName, Size: size, Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				_, _ = w.Write(b)
			}
			_ = w.Close()
			_ = f.Close()
			target := filepath.Join(t.TempDir(), "output")
			if err = Publish(context.Background(), archive, target); err == nil {
				t.Fatal("bad directory bundle published")
			}
			if _, err = os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("bad tree visible as complete")
			}
		})
	}
}

func TestDirectorySnapshotAndTamperedFinalTree(t *testing.T) {
	source := filepath.Join(t.TempDir(), "folder")
	_ = os.Mkdir(source, 0700)
	file := filepath.Join(source, "file.txt")
	_ = os.WriteFile(file, []byte("original"), 0600)
	archive := filepath.Join(t.TempDir(), "data.tar")
	if _, err := Create(context.Background(), source, archive); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(file, []byte("new data"), 0600)
	target := filepath.Join(t.TempDir(), "out")
	if err := Publish(context.Background(), archive, target); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(filepath.Join(target, "file.txt"))
	if string(actual) != "original" {
		t.Fatal("bundle follows mutable source")
	}
	_ = os.WriteFile(filepath.Join(target, "file.txt"), []byte("tampered"), 0600)
	if err := Verify(context.Background(), archive, target); err == nil {
		t.Fatal("changed destination trusted")
	}
}
