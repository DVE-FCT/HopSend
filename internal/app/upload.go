package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func UploadDestination(agentURL, share, target string) string {
	return agentURL + "/shares/" + url.PathEscape(share) + "/" + url.PathEscape(target)
}

func PrepareUpload(ctx context.Context, r DownloadRequest, source, target string, chunkSize int64) (DownloadRequest, error) {
	if r.Database == "" {
		r.Database = DefaultDatabase()
	}
	absolute, err := filepath.Abs(r.Database)
	if err != nil {
		return r, err
	}
	r.Database = absolute
	if target == "" {
		target = filepath.Base(source)
	}
	if err = filesystem.SafeLeaf(target); err != nil {
		return r, err
	}
	r.SnapshotDirectory = filepath.Join(filepath.Dir(absolute), "upload-content")
	m, err := manifest.Publish(ctx, source, r.SnapshotDirectory, r.ShareID, chunkSize)
	if err != nil {
		return r, err
	}
	r.Direction, r.ManifestID, r.UploadTarget = "upload", m.ManifestID, target
	r.Destination = UploadDestination(r.Agent.URL, r.ShareID, target)
	return r, nil
}

type timedSink struct {
	*transport.Client
	timeout time.Duration
}
type activityReader struct {
	reader   io.Reader
	timeout  time.Duration
	watchdog *time.Timer
}

func (r *activityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.watchdog.Reset(r.timeout)
	}
	return n, err
}
func (s *timedSink) WriteRange(ctx context.Context, path string, offset, length int64, input io.Reader) (int64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(s.timeout, func() { cancel(context.DeadlineExceeded) })
	defer watchdog.Stop()
	n, err := s.Client.WriteRange(ctx, path, offset, length, &activityReader{reader: input, timeout: s.timeout, watchdog: watchdog})
	if ctx.Err() != nil {
		return n, context.Cause(ctx)
	}
	return n, err
}

func Upload(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
	if r.Direction != "upload" || !manifest.ValidID(r.ManifestID) || r.Workers < 1 || r.Workers > 8 || r.Retries < 0 || r.Retries > 100 || r.RetryDelay < 0 || r.RetryDelay > time.Minute {
		return engine.DownloadResult{}, errors.New("上传任务参数无效")
	}
	if r.Database == "" {
		r.Database = DefaultDatabase()
	}
	for _, path := range []*string{&r.Database, &r.SnapshotDirectory, &r.Agent.CACertificate, &r.Agent.TokenFile, &r.SSH.PrivateKeyPath, &r.SSH.KnownHostsPath} {
		if *path == "" {
			return engine.DownloadResult{}, errors.New("上传路径引用不能为空")
		}
		p, err := filepath.Abs(*path)
		if err != nil {
			return engine.DownloadResult{}, err
		}
		*path = p
	}
	if r.Destination != UploadDestination(r.Agent.URL, r.ShareID, r.UploadTarget) || filesystem.SafeLeaf(r.UploadTarget) != nil {
		return engine.DownloadResult{}, errors.New("上传目标身份不一致")
	}
	if r.ReadTimeout <= 0 {
		r.ReadTimeout = 2 * time.Minute
	}
	return runWithRetry(ctx, r, uploadAttempt)
}

func uploadAttempt(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
	root, err := os.OpenRoot(r.SnapshotDirectory)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	defer root.Close()
	m, err := manifest.Load(root, r.ManifestID)
	if err != nil || m.ShareID != r.ShareID {
		return engine.DownloadResult{}, errors.New("本机上传快照清单不可用或共享不一致")
	}
	client, err := nodeclient.New(r.Agent)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	defer client.Close()
	session, err := client.CreateUpload(ctx, v1.CreateUpload{RequestID: engine.TaskID(m.ManifestID, r.Destination), ShareID: r.ShareID, Target: r.UploadTarget, Manifest: m})
	if err != nil {
		return engine.DownloadResult{}, err
	}
	store, err := sqlite.Open(r.Database)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	defer store.Close()
	source, err := json.Marshal(r)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	sum := sha256.Sum256([]byte(r.Destination))
	lockPath := filepath.Join(filepath.Dir(r.Database), "upload-"+hex.EncodeToString(sum[:])+".lock")
	return engine.Upload(ctx, engine.UploadOptions{Manifest: m, Snapshot: filepath.Join(r.SnapshotDirectory, "objects", m.ContentID), Destination: r.Destination, LockPath: lockPath, Session: session, Workers: r.Workers, Receiver: client, Journal: store, Source: source, OnProgress: r.OnProgress, OpenSink: func(ctx context.Context) (engine.RangeSink, error) {
		c, err := transport.Dial(ctx, r.SSH)
		if err != nil {
			return nil, err
		}
		return &timedSink{Client: c, timeout: r.ReadTimeout}, nil
	}})
}
