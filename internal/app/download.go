package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

type DownloadRequest struct {
	Agent                            nodeclient.Config
	SSH                              transport.Config
	ShareID, ManifestID, Destination string
	Workers                          int
	ReadTimeout                      time.Duration
	Database                         string
	Retries                          int
	RetryDelay                       time.Duration
	OnProgress                       func(engine.Progress) `json:"-"`
}

func DefaultDatabase() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "HopSend", "cli", "state.db")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "hopsend-tasks.sqlite"
	}
	return filepath.Join(dir, "HopSend", "tasks.sqlite")
}

func Resume(ctx context.Context, database, id string, progress func(engine.Progress)) (engine.DownloadResult, error) {
	store, err := sqlite.Open(database)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	task, err := store.Get(ctx, id)
	_ = store.Close()
	if err != nil {
		return engine.DownloadResult{}, err
	}
	if task.State == "cancelled" {
		return engine.DownloadResult{}, errors.New("任务已取消；如需重新下载，请显式运行 download")
	}
	var request DownloadRequest
	if err = json.Unmarshal(task.Source, &request); err != nil {
		return engine.DownloadResult{}, err
	}
	request.Database, request.OnProgress = database, progress
	if request.ManifestID != task.Manifest.ManifestID || request.Destination != task.Destination {
		return engine.DownloadResult{}, errors.New("任务源引用与持久化身份不一致")
	}
	return Download(ctx, request)
}

type timedSource struct {
	*transport.Client
	timeout time.Duration
}

func (s *timedSource) ReadRange(ctx context.Context, path string, offset, length int64, w io.Writer) (int64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watchdog := time.AfterFunc(s.timeout, func() { cancel(context.DeadlineExceeded) })
	defer watchdog.Stop()
	n, err := s.Client.ReadRange(ctx, path, offset, length, &activityWriter{target: w, timeout: s.timeout, watchdog: watchdog})
	if ctx.Err() != nil {
		return n, context.Cause(ctx)
	}
	return n, err
}

type activityWriter struct {
	target   io.Writer
	timeout  time.Duration
	watchdog *time.Timer
}

func (w *activityWriter) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	if n > 0 {
		w.watchdog.Reset(w.timeout)
	}
	return n, err
}

func Download(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
	if r.Database == "" {
		r.Database = DefaultDatabase()
	}
	for _, path := range []*string{&r.Database, &r.Destination, &r.Agent.CACertificate, &r.Agent.TokenFile, &r.SSH.PrivateKeyPath, &r.SSH.KnownHostsPath} {
		if *path == "" {
			return engine.DownloadResult{}, errors.New("任务的文件路径不能为空")
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return engine.DownloadResult{}, err
		}
		*path = absolute
	}
	if r.Retries < 0 || r.Retries > 100 || r.RetryDelay < 0 || r.RetryDelay > time.Minute {
		return engine.DownloadResult{}, errors.New("重试次数必须为 0–100，初始退避必须为 0–1 分钟")
	}
	return runWithRetry(ctx, r, downloadAttempt)
}

func downloadAttempt(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
	client, err := nodeclient.New(r.Agent)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	defer client.Close()
	published, err := client.Manifest(ctx, r.ShareID, r.ManifestID)
	if err != nil {
		return engine.DownloadResult{}, err
	}
	if r.ReadTimeout <= 0 {
		r.ReadTimeout = 2 * time.Minute
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
	return engine.Download(ctx, engine.DownloadOptions{Manifest: published.Manifest, RemotePath: published.SFTPPath, Destination: r.Destination, Workers: r.Workers, OnProgress: r.OnProgress, Journal: store, Source: source,
		OpenSource: func(ctx context.Context) (engine.RangeSource, error) {
			c, err := transport.Dial(ctx, r.SSH)
			if err != nil {
				return nil, err
			}
			return &timedSource{Client: c, timeout: r.ReadTimeout}, nil
		}})
}
