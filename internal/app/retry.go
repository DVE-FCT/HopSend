package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"io"
	"net"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	pkgsftp "github.com/pkg/sftp"
)

func retryable(err error) bool {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) || errors.As(err, &certificate) {
		return false
	}
	var status *nodeclient.HTTPError
	if errors.As(err, &status) {
		return status.StatusCode == 408 || status.StatusCode == 429 || status.StatusCode >= 500
	}
	var sftpStatus *pkgsftp.StatusError
	if errors.As(err, &sftpStatus) {
		return sftpStatus.Code == 6 || sftpStatus.Code == 7
	}
	if errors.Is(err, pkgsftp.ErrSSHFxConnectionLost) || errors.Is(err, pkgsftp.ErrSSHFxNoConnection) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded)
}

func setExistingTaskState(ctx context.Context, path, id, state string) error {
	store, err := sqlite.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err = store.Get(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	return store.SetState(ctx, id, state)
}

// Download's attempt function returns only after every old worker has exited.
// Retrying whole attempts deliberately reuses the durable recovery validator.
func runWithRetry(ctx context.Context, r DownloadRequest, attempt func(context.Context, DownloadRequest) (engine.DownloadResult, error)) (engine.DownloadResult, error) {
	var last engine.Progress
	id := engine.TaskID(r.ManifestID, r.Destination)
	original := r.OnProgress
	r.OnProgress = func(p engine.Progress) {
		last = p
		if original != nil {
			original(p)
		}
	}
	delay := r.RetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	for retry := 0; ; retry++ {
		result, err := attempt(ctx, r)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		if retry >= r.Retries || !retryable(err) {
			return result, err
		}
		if saveErr := setExistingTaskState(ctx, r.Database, id, "waiting_network"); saveErr != nil {
			return result, errors.Join(err, saveErr)
		}
		last.TaskID, last.State = id, "waiting_network"
		if original != nil {
			original(last)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, context.Cause(ctx)
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
