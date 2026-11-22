package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	pkgsftp "github.com/pkg/sftp"
)

func TestDataActivityKeepsSlowStreamAliveButIdleStreamExpires(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timeout := 100 * time.Millisecond
	timer := time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded) })
	defer timer.Stop()
	var bytesWritten bytes.Buffer
	w := activityWriter{target: &bytesWritten, timeout: timeout, watchdog: timer}
	for range 8 {
		time.Sleep(25 * time.Millisecond)
		if _, err := w.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatal("active stream cancelled by total block duration")
		}
	}
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			t.Fatal(context.Cause(ctx))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle stream never timed out")
	}
	if bytesWritten.Len() != 32 {
		t.Fatal(bytesWritten.Len())
	}
}

func TestRetryClassificationKeepsTrustAndIntegrityFailuresTerminal(t *testing.T) {
	for _, test := range []struct {
		err  error
		want bool
	}{
		{&url.Error{Op: "Get", URL: "https://fixture", Err: x509.UnknownAuthorityError{}}, false},
		{&url.Error{Op: "Get", URL: "https://fixture", Err: x509.HostnameError{}}, false},
		{&nodeclient.HTTPError{StatusCode: 401}, false},
		{&nodeclient.HTTPError{StatusCode: 404}, false},
		{&nodeclient.HTTPError{StatusCode: 503}, true},
		{&nodeclient.HTTPError{StatusCode: 507}, false},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, true},
		{fmt.Errorf("ssh handshake: %w", io.EOF), true},
		{context.DeadlineExceeded, true},
		{context.Canceled, false},
		{fmt.Errorf("range open: %w", pkgsftp.ErrSSHFxConnectionLost), true},
		{pkgsftp.ErrSSHFxPermissionDenied, false},
		{io.ErrShortWrite, false},
		{&os.PathError{Op: "write", Path: "fixture", Err: errors.New("disk full")}, false},
		{errors.New("分块哈希不一致"), false},
	} {
		if got := retryable(test.err); got != test.want {
			t.Errorf("%T retry=%v want %v", test.err, got, test.want)
		}
	}
}

func TestRetryIsBoundedAndCancellationStopsBackoff(t *testing.T) {
	request := DownloadRequest{ManifestID: strings.Repeat("a", 64), Destination: filepath.Join(t.TempDir(), "out.bin"), Database: filepath.Join(t.TempDir(), "tasks.sqlite"), Retries: 2, RetryDelay: time.Millisecond}
	attempts := 0
	fail := func(context.Context, DownloadRequest) (engine.DownloadResult, error) {
		attempts++
		return engine.DownloadResult{}, &net.OpError{Op: "dial", Err: io.EOF}
	}
	if _, err := runWithRetry(context.Background(), request, fail); err == nil || attempts != 3 {
		t.Fatal(attempts, err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	request.RetryDelay = time.Minute
	request.OnProgress = func(p engine.Progress) {
		if p.State == "waiting_network" {
			cancel(ErrPaused)
		}
	}
	attempts = 0
	started := time.Now()
	if _, err := runWithRetry(ctx, request, fail); !errors.Is(err, ErrPaused) || attempts != 1 || time.Since(started) > time.Second {
		t.Fatal(attempts, err)
	}
}

func TestManagerStopWaitsForOldWriterAndRetainsCompletedRace(t *testing.T) {
	request := DownloadRequest{ManifestID: strings.Repeat("a", 64), Destination: filepath.Join(t.TempDir(), "out.bin"), Workers: 1}
	m := NewManager(filepath.Join(t.TempDir(), "tasks.sqlite"), nil)
	started := make(chan struct{})
	released := make(chan struct{})
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		close(released)
		return engine.DownloadResult{}, ctx.Err()
	}
	id, err := m.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = m.Start(context.Background(), request); err == nil {
		t.Fatal("duplicate runner")
	}
	if err = m.Pause(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	default:
		t.Fatal("pause returned while writer still active")
	}
	if status, _ := m.Status(id); status.State != "paused" {
		t.Fatal(status)
	}
	if _, err = m.Wait(context.Background(), id); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
	m.run = func(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
		<-ctx.Done()
		return engine.DownloadResult{Path: r.Destination}, nil
	}
	id, err = m.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if status, _ := m.Status(id); status.State != "completed" {
		t.Fatal("late cancel hid successful publication", status)
	}
	if err = m.Pause(context.Background(), id); err != nil {
		t.Fatal("late pause should be harmless for this process's finished task", err)
	}
}
