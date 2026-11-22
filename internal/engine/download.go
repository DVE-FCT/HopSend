package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type RangeSource interface {
	ReadRange(context.Context, string, int64, int64, io.Writer) (int64, error)
	Close() error
}
type SourceFactory func(context.Context) (RangeSource, error)
type Progress struct {
	State           string `json:"state"`
	VerifiedBytes   int64  `json:"verified_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	CompletedChunks int    `json:"completed_chunks"`
}
type DownloadOptions struct {
	Manifest    v1.Manifest
	RemotePath  string
	Destination string
	Workers     int
	OpenSource  SourceFactory
	OnProgress  func(Progress)
}
type DownloadResult struct {
	ManifestID string `json:"manifest_id"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Chunks     int    `json:"chunks"`
}

type chunkWriter struct {
	file              *os.File
	offset, remaining int64
	hash              hash.Hash
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("源端返回超过分块范围的数据")
	}
	n, err := w.file.WriteAt(p, w.offset)
	if n > 0 {
		_, _ = w.hash.Write(p[:n])
		w.offset += int64(n)
		w.remaining -= int64(n)
	}
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

// Download currently starts a new task. P1.2 adds durable recovery; an existing
// staging file is intentionally not overwritten by this first-stage operation.
func Download(parent context.Context, opts DownloadOptions) (DownloadResult, error) {
	var result DownloadResult
	if err := manifest.Validate(opts.Manifest); err != nil {
		return result, err
	}
	if opts.Workers < 1 || opts.Workers > 8 || opts.OpenSource == nil || opts.RemotePath == "" || opts.Destination == "" {
		return result, errors.New("下载参数无效")
	}
	if err := parent.Err(); err != nil {
		return result, err
	}
	destination, err := filepath.Abs(opts.Destination)
	if err != nil {
		return result, err
	}
	if _, err = os.Lstat(destination); err == nil {
		return result, errors.New("目标已存在，拒绝覆盖")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return result, err
	}
	staging := destination + "." + opts.Manifest.ManifestID[:16] + ".partial"
	file, err := os.OpenFile(staging, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, fmt.Errorf("创建唯一暂存文件: %w", err)
	}
	defer file.Close()
	if err = file.Truncate(opts.Manifest.SizeBytes); err != nil {
		return result, fmt.Errorf("分配目标文件空间: %w", err)
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	queue := make(chan v1.Chunk, len(opts.Manifest.Chunks))
	for _, chunk := range opts.Manifest.Chunks {
		queue <- chunk
	}
	close(queue)
	progress := Progress{State: "downloading", TotalBytes: opts.Manifest.SizeBytes}
	var mu sync.Mutex
	var wg sync.WaitGroup
	emit := func() {
		if opts.OnProgress != nil {
			opts.OnProgress(progress)
		}
	}
	emit()
	for range min(opts.Workers, len(opts.Manifest.Chunks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			source, err := opts.OpenSource(ctx)
			if err != nil {
				cancel(err)
				return
			}
			defer source.Close()
			for chunk := range queue {
				if ctx.Err() != nil {
					return
				}
				h := sha256.New()
				writer := &chunkWriter{file: file, offset: chunk.Offset, remaining: chunk.Length, hash: h}
				n, err := source.ReadRange(ctx, opts.RemotePath, chunk.Offset, chunk.Length, writer)
				if err != nil {
					cancel(fmt.Errorf("读取分块 %d: %w", chunk.Index, err))
					return
				}
				if n != chunk.Length || writer.remaining != 0 || hex.EncodeToString(h.Sum(nil)) != chunk.SHA256 {
					cancel(fmt.Errorf("分块 %d 哈希或长度不一致", chunk.Index))
					return
				}
				if err = file.Sync(); err != nil {
					cancel(fmt.Errorf("同步分块: %w", err))
					return
				}
				mu.Lock()
				progress.VerifiedBytes += chunk.Length
				progress.CompletedChunks++
				emit()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if err = context.Cause(ctx); err != nil {
		return result, err
	}
	progress.State = "verifying"
	emit()
	if err = file.Sync(); err != nil {
		return result, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	actual, err := manifest.HashReader(ctx, file)
	if err != nil {
		return result, err
	}
	if actual != opts.Manifest.ContentID {
		return result, errors.New("最终全文件 SHA-256 不一致，未发布文件")
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = filesystem.PublishNoReplace(staging, destination); err != nil {
		return result, fmt.Errorf("发布文件失败，目标可能已被创建: %w", err)
	}
	progress.State = "completed"
	emit()
	return DownloadResult{ManifestID: opts.Manifest.ManifestID, Path: destination, Bytes: opts.Manifest.SizeBytes, SHA256: actual, Chunks: len(opts.Manifest.Chunks)}, nil
}
