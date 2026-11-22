package app

import (
	"context"
	"io"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

type DownloadRequest struct {
	Agent                            nodeclient.Config
	SSH                              transport.Config
	ShareID, ManifestID, Destination string
	Workers                          int
	ReadTimeout                      time.Duration
	OnProgress                       func(engine.Progress)
}

type timedSource struct {
	*transport.Client
	timeout time.Duration
}

func (s *timedSource) ReadRange(ctx context.Context, path string, offset, length int64, w io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.Client.ReadRange(ctx, path, offset, length, w)
}

func Download(ctx context.Context, r DownloadRequest) (engine.DownloadResult, error) {
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
	return engine.Download(ctx, engine.DownloadOptions{Manifest: published.Manifest, RemotePath: published.SFTPPath, Destination: r.Destination, Workers: r.Workers, OnProgress: r.OnProgress,
		OpenSource: func(ctx context.Context) (engine.RangeSource, error) {
			c, err := transport.Dial(ctx, r.SSH)
			if err != nil {
				return nil, err
			}
			return &timedSource{Client: c, timeout: r.ReadTimeout}, nil
		}})
}
