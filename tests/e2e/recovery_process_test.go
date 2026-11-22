package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

type diskSource struct {
	root   string
	onRead func(int64)
}

type rejectConfirmation struct{ engine.Journal }

func (s rejectConfirmation) SetChunk(ctx context.Context, id string, index int, confirmed bool) error {
	if confirmed {
		return errors.New("injected SQLite commit failure")
	}
	return s.Journal.SetChunk(ctx, id, index, confirmed)
}

func TestFailedConfirmationDoesNotReportVerifiedProgress(t *testing.T) {
	root := recoveryFixture(t)
	opts, db := recoveryOptions(t, root)
	defer db.Close()
	opts.Journal = rejectConfirmation{Journal: db}
	var largest int64
	opts.OnProgress = func(p engine.Progress) { largest = max(largest, p.VerifiedBytes) }
	if _, err := engine.Download(context.Background(), opts); err == nil {
		t.Fatal("commit failure ignored")
	}
	tasks, err := db.List(context.Background())
	if err != nil || len(tasks) != 1 || len(tasks[0].Confirmed) != 0 || largest != 0 {
		t.Fatal("uncommitted data reported confirmed", tasks, largest, err)
	}
	if _, err = os.Stat(opts.Destination); !os.IsNotExist(err) {
		t.Fatal("failed commit published file")
	}
	opts.Journal = db
	result, err := engine.Download(context.Background(), opts)
	if err != nil || result.ReusedChunks != 0 {
		t.Fatal(result, err)
	}
}

func (s *diskSource) Close() error { return nil }
func (s *diskSource) ReadRange(ctx context.Context, _ string, offset, length int64, w io.Writer) (int64, error) {
	if s.onRead != nil {
		s.onRead(offset)
	}
	f, err := os.Open(filepath.Join(s.root, "source.bin"))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	return io.Copy(w, io.NewSectionReader(f, offset, length))
}

func recoveryOptions(t *testing.T, root string) (engine.DownloadOptions, *sqlite.Store) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m v1.Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(root, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return engine.DownloadOptions{Manifest: m, RemotePath: "/snapshot", Destination: filepath.Join(root, "result.bin"), Workers: 1, Journal: db, OpenSource: func(context.Context) (engine.RangeSource, error) { return &diskSource{root: root}, nil }}, db
}

// Executed as a real second OS process. os.Exit skips all defer cleanup and
// leaves SQLite/files exactly at the requested commit boundary.
func TestRecoveryChild(t *testing.T) {
	root := os.Getenv("HOPSEND_RECOVERY_ROOT")
	if root == "" {
		t.Skip("subprocess only")
	}
	opts, db := recoveryOptions(t, root)
	defer db.Close()
	point := os.Getenv("HOPSEND_CRASH_POINT")
	if point == "transaction" {
		tasks, err := db.List(context.Background())
		if err != nil || len(tasks) != 1 {
			t.Fatal(err)
		}
		connection, err := sql.Open("sqlite", filepath.Join(root, "tasks.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := connection.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO chunks VALUES(?,?)`, tasks[0].ID, 1); err != nil {
			t.Fatal(err)
		}
		os.Exit(73)
	}
	opts.Boundary = func(p string, index int) error {
		if p == point && (index == 1 || index == -1) {
			os.Exit(73)
		}
		if point == "hold" && p == "written" {
			if err := os.WriteFile(filepath.Join(root, "locked"), []byte("ready"), 0600); err != nil {
				return err
			}
			time.Sleep(30 * time.Second)
		}
		return nil
	}
	if _, err := engine.Download(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
}

func TestActualExitRollsBackUncommittedChunkTransaction(t *testing.T) {
	root := recoveryFixture(t)
	crash(t, root, "synced")
	crash(t, root, "transaction")
	opts, db := recoveryOptions(t, root)
	defer db.Close()
	tasks, err := db.List(context.Background())
	if err != nil || len(tasks) != 1 || len(tasks[0].Confirmed) != 1 {
		t.Fatal("uncommitted block became confirmed", tasks, err)
	}
	result, err := engine.Download(context.Background(), opts)
	if err != nil || result.ReusedChunks != 1 {
		t.Fatal(result, err)
	}
}

func recoveryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	data := bytes.Repeat([]byte("recover large ranges"), 14000)
	if err := os.WriteFile(filepath.Join(root, "source.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Publish(context.Background(), filepath.Join(root, "source.bin"), filepath.Join(root, "content"), "files", manifest.MinChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "content", "objects", m.ContentID), 0600) })
	b, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func childCommand(t *testing.T, root, point string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryChild$")
	cmd.Env = append(os.Environ(), "HOPSEND_RECOVERY_ROOT="+root, "HOPSEND_CRASH_POINT="+point)
	return cmd
}

func crash(t *testing.T, root, point string) {
	t.Helper()
	output, err := childCommand(t, root, point).CombinedOutput()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 73 {
		t.Fatalf("expected abrupt process exit: %v %s", err, output)
	}
}

func TestActualProcessCrashBoundariesRecoverWithoutMissingBytes(t *testing.T) {
	for _, point := range []string{"written", "synced", "confirmed", "publishing", "renamed", "completed"} {
		t.Run(point, func(t *testing.T) {
			root := recoveryFixture(t)
			crash(t, root, point)
			opts, db := recoveryOptions(t, root)
			defer db.Close()
			tasks, err := db.List(context.Background())
			if err != nil || len(tasks) != 1 {
				t.Fatal(tasks, err)
			}
			expected := 1
			if point == "confirmed" {
				expected = 2
			}
			if point == "publishing" || point == "renamed" || point == "completed" {
				expected = len(opts.Manifest.Chunks)
			}
			if len(tasks[0].Confirmed) != expected {
				t.Fatalf("durability boundary %s: got %v", point, tasks[0].Confirmed)
			}
			result, err := engine.Download(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.ReusedChunks != expected {
				t.Fatalf("reused %d want %d", result.ReusedChunks, expected)
			}
			actual, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			source, _ := os.ReadFile(filepath.Join(root, "source.bin"))
			if !bytes.Equal(actual, source) {
				t.Fatal("recovery bytes differ")
			}
			stored, err := db.Get(context.Background(), result.TaskID)
			if err != nil || stored.State != "completed" {
				t.Fatal(stored.State, err)
			}
		})
	}
}

func TestRecoveryRechecksCorruptTruncatedAndMissingStaging(t *testing.T) {
	for _, fault := range []string{"corrupt", "truncate", "missing"} {
		t.Run(fault, func(t *testing.T) {
			root := recoveryFixture(t)
			crash(t, root, "confirmed")
			opts, db := recoveryOptions(t, root)
			defer db.Close()
			tasks, err := db.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			staging := tasks[0].Staging
			switch fault {
			case "corrupt":
				f, err := os.OpenFile(staging, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteAt([]byte("broken"), 0)
				if err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
			case "truncate":
				if err = os.Truncate(staging, 3); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err = os.Remove(staging); err != nil {
					t.Fatal(err)
				}
			}
			var offsets []int64
			opts.OpenSource = func(context.Context) (engine.RangeSource, error) {
				return &diskSource{root: root, onRead: func(offset int64) { offsets = append(offsets, offset) }}, nil
			}
			result, err := engine.Download(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if fault == "corrupt" {
				want = 1
			}
			if result.ReusedChunks != want {
				t.Fatalf("trusted damaged disk: %d", result.ReusedChunks)
			}
			if len(offsets) != len(opts.Manifest.Chunks)-want {
				t.Fatalf("unexpected range reads: %v", offsets)
			}
			if fault == "corrupt" {
				for _, offset := range offsets {
					if offset == opts.Manifest.Chunks[1].Offset {
						t.Fatal("valid confirmed block downloaded again")
					}
				}
				if len(offsets) == 0 || offsets[0] != 0 {
					t.Fatal("damaged block not fetched")
				}
			}
			actual, _ := os.ReadFile(result.Path)
			source, _ := os.ReadFile(filepath.Join(root, "source.bin"))
			if !bytes.Equal(actual, source) {
				t.Fatal("repaired bytes differ")
			}
		})
	}
}

func TestCrossProcessExclusiveWriterAndDeadOwnerRelease(t *testing.T) {
	root := recoveryFixture(t)
	cmd := childCommand(t, root, "hold")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "locked")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child lock not ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	opts, db := recoveryOptions(t, root)
	defer db.Close()
	if _, err := engine.Download(context.Background(), opts); err == nil {
		t.Fatal("second writer acquired target")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := engine.Download(context.Background(), opts); err != nil {
		t.Fatal("dead owner lock not released:", err)
	}
}

func TestRecoveryCannotAdoptOrOverwriteConflictingDestination(t *testing.T) {
	for _, point := range []string{"confirmed", "publishing", "renamed"} {
		t.Run(point, func(t *testing.T) {
			root := recoveryFixture(t)
			crash(t, root, point)
			opts, db := recoveryOptions(t, root)
			defer db.Close()
			value := []byte("foreign user data")
			if point == "publishing" {
				value, _ = os.ReadFile(filepath.Join(root, "source.bin"))
			}
			if err := os.WriteFile(opts.Destination, value, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Download(context.Background(), opts); err == nil {
				t.Fatal("destination conflict accepted")
			}
			actual, _ := os.ReadFile(opts.Destination)
			if !bytes.Equal(actual, value) {
				t.Fatal("foreign destination modified")
			}
		})
	}
}
