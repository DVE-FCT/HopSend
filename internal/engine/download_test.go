package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type memorySource struct {
	data    []byte
	corrupt bool
}

func (s *memorySource) Close() error { return nil }
func (s *memorySource) ReadRange(ctx context.Context, _ string, offset, length int64, w io.Writer) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	value := s.data[offset : offset+length]
	if s.corrupt {
		value = bytes.Clone(value)
		value[0] ^= 1
	}
	n, err := w.Write(value)
	return int64(n), err
}

func downloadFixture(t *testing.T, data []byte) DownloadOptions {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	m, err := manifest.Publish(context.Background(), source, store, "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(store, "objects", m.ContentID), 0600) })
	return DownloadOptions{Manifest: m, RemotePath: "/snapshot", Destination: filepath.Join(root, "out", "result.bin"), Workers: 2, OpenSource: func(context.Context) (RangeSource, error) { return &memorySource{data: data}, nil }}
}

func TestDownloadVerifiesChunksAndFinalContent(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("tail"), bytes.Repeat([]byte("large sample"), 100000)} {
		opts := downloadFixture(t, data)
		var states []Progress
		opts.OnProgress = func(p Progress) { states = append(states, p) }
		result, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(result.Path)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal(err)
		}
		if result.Bytes != int64(len(data)) || states[len(states)-1].State != "completed" {
			t.Fatal(result, states)
		}
		for i := 1; i < len(states); i++ {
			if states[i].VerifiedBytes < states[i-1].VerifiedBytes || states[i].VerifiedBytes > int64(len(data)) {
				t.Fatal("invalid progress")
			}
		}
	}
}

func TestBadChunkAndBadFinalHashNeverPublish(t *testing.T) {
	data := bytes.Repeat([]byte("sample"), 20000)
	t.Run("bad chunk", func(t *testing.T) {
		opts := downloadFixture(t, data)
		opts.OpenSource = func(context.Context) (RangeSource, error) { return &memorySource{data: data, corrupt: true}, nil }
		if _, err := Download(context.Background(), opts); err == nil {
			t.Fatal("corrupt chunk accepted")
		}
		if _, err := os.Stat(opts.Destination); !os.IsNotExist(err) {
			t.Fatal("unverified file published")
		}
	})
	t.Run("bad final hash", func(t *testing.T) {
		opts := downloadFixture(t, data)
		opts.Manifest.ContentID = strings.Repeat("f", 64)
		opts.Manifest.ManifestID = manifest.ID(opts.Manifest)
		if _, err := Download(context.Background(), opts); err == nil {
			t.Fatal("bad final hash accepted")
		}
		if _, err := os.Stat(opts.Destination); !os.IsNotExist(err) {
			t.Fatal("unverified file published")
		}
	})
}

func TestDestinationConflictCannotOverwrite(t *testing.T) {
	opts := downloadFixture(t, []byte("download"))
	opts.OnProgress = func(p Progress) {
		if p.State == "verifying" {
			if err := os.WriteFile(opts.Destination, []byte("keep user data"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := Download(context.Background(), opts); err == nil {
		t.Fatal("racing destination overwritten")
	}
	actual, err := os.ReadFile(opts.Destination)
	if err != nil || string(actual) != "keep user data" {
		t.Fatal("user file changed", err)
	}
}

func TestCancelledDownloadCannotPublish(t *testing.T) {
	opts := downloadFixture(t, bytes.Repeat([]byte{7}, 200000))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.OnProgress = func(p Progress) {
		if p.CompletedChunks > 0 {
			cancel()
		}
	}
	if _, err := Download(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation", err)
	}
	if _, err := os.Stat(opts.Destination); !os.IsNotExist(err) {
		t.Fatal("cancelled file published")
	}
}

func TestRejectsInvalidInputBeforeCreatingFile(t *testing.T) {
	opts := downloadFixture(t, []byte("sample"))
	opts.Manifest.Chunks = []v1.Chunk{}
	if _, err := Download(context.Background(), opts); err == nil {
		t.Fatal("invalid manifest accepted")
	}
	if _, err := os.Stat(filepath.Dir(opts.Destination)); !os.IsNotExist(err) {
		t.Fatal("invalid input changed output directory")
	}
}
