package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/folder"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type RangeSource interface {
	ReadRange(context.Context, string, int64, int64, io.Writer) (int64, error)
	Close() error
}
type SourceFactory func(context.Context) (RangeSource, error)
type Progress struct {
	TaskID          string `json:"task_id,omitempty"`
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
	Journal     Journal
	Source      json.RawMessage
	// Boundary supports deterministic crash-consistency tests. Production leaves
	// it nil. A hook runs after the named operation has completed.
	Boundary func(point string, chunk int) error
}
type DownloadResult struct {
	TaskID       string `json:"task_id,omitempty"`
	ReusedChunks int    `json:"reused_chunks"`
	ManifestID   string `json:"manifest_id"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	Chunks       int    `json:"chunks"`
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

// Download revalidates every recorded block before reusing it. The destination
// lock spans recovery, workers, publication and the final journal transaction.
func Download(parent context.Context, opts DownloadOptions) (result DownloadResult, returnedErr error) {
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
	if err = filesystem.CheckComponents(destination); err != nil {
		return result, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return result, err
	}
	lock, err := filesystem.Lock(destination + ".hopsend.lock")
	if err != nil {
		return result, err
	}
	defer lock.Close()
	task := Task{ID: TaskID(opts.Manifest.ManifestID, destination), Manifest: opts.Manifest, RemotePath: opts.RemotePath, Destination: destination, State: "pending", Source: opts.Source}
	staging := destination + "." + opts.Manifest.ManifestID[:16] + ".partial"
	if opts.Journal != nil {
		random := make([]byte, 8)
		if _, err = rand.Read(random); err != nil {
			return result, err
		}
		task.Staging = destination + "." + hex.EncodeToString(random) + ".partial"
		if len(task.Source) == 0 {
			task.Source = json.RawMessage(`{}`)
		}
		task, err = opts.Journal.Prepare(parent, task)
		if err != nil {
			return result, err
		}
		staging = task.Staging
		if filepath.Dir(staging) != filepath.Dir(destination) || staging == destination {
			return result, errors.New("任务暂存路径必须位于目标同目录")
		}
	}
	setState := func(state string) error {
		if opts.Journal != nil {
			if err := opts.Journal.SetState(context.WithoutCancel(parent), task.ID, state); err != nil {
				return err
			}
		}
		task.State = state
		return nil
	}
	boundary := func(point string, chunk int) error {
		if opts.Boundary != nil {
			return opts.Boundary(point, chunk)
		}
		return nil
	}
	defer func() {
		if returnedErr != nil && task.State != "publishing" && task.State != "completed" {
			returnedErr = errors.Join(returnedErr, setState("interrupted"))
		}
	}()
	if info, statErr := os.Lstat(destination); statErr == nil {
		if opts.Manifest.PayloadKind == "folder" {
			if opts.Journal == nil || (task.State != "publishing" && task.State != "completed") || !info.IsDir() || filesystem.IsReparse(info) {
				return result, errors.New("目标文件夹已存在，拒绝覆盖")
			}
			if _, e := os.Lstat(staging + ".folder"); !os.IsNotExist(e) {
				return result, errors.New("展开暂存与正式目录同时存在，拒绝冒认外部目录")
			}
			if err = folder.VerifyContent(parent, staging, destination, opts.Manifest.ContentID); err != nil {
				return result, err
			}
			if err = setState("completed"); err != nil {
				return result, err
			}
			if opts.OnProgress != nil {
				opts.OnProgress(Progress{TaskID: task.ID, State: "completed", TotalBytes: opts.Manifest.SizeBytes, VerifiedBytes: opts.Manifest.SizeBytes, CompletedChunks: len(opts.Manifest.Chunks)})
			}
			return DownloadResult{TaskID: task.ID, ManifestID: opts.Manifest.ManifestID, Path: destination, Bytes: opts.Manifest.SizeBytes, SHA256: opts.Manifest.ContentID, Chunks: len(opts.Manifest.Chunks), ReusedChunks: len(opts.Manifest.Chunks)}, nil
		}
		// Only a recorded publication can be reconciled. A same-name file during
		// downloading remains a conflict, even if its bytes happen to match.
		if opts.Journal == nil || (task.State != "publishing" && task.State != "completed") || !info.Mode().IsRegular() || info.Size() != opts.Manifest.SizeBytes {
			return result, errors.New("目标已存在，拒绝覆盖")
		}
		file, err := os.Open(destination)
		if _, e := os.Lstat(staging); !os.IsNotExist(e) {
			if file != nil {
				_ = file.Close()
			}
			return result, errors.New("暂存与目标同时存在，不能将外部文件当作本任务发布结果")
		}
		if err != nil {
			return result, err
		}
		digest, err := manifest.HashReader(parent, file)
		_ = file.Close()
		if err != nil {
			return result, err
		}
		if digest != opts.Manifest.ContentID {
			return result, errors.New("已发布目标校验失败，拒绝覆盖")
		}
		if err = setState("completed"); err != nil {
			return result, err
		}
		if opts.OnProgress != nil {
			opts.OnProgress(Progress{TaskID: task.ID, State: "completed", TotalBytes: opts.Manifest.SizeBytes, VerifiedBytes: opts.Manifest.SizeBytes, CompletedChunks: len(opts.Manifest.Chunks)})
		}
		return DownloadResult{TaskID: task.ID, ManifestID: opts.Manifest.ManifestID, Path: destination, Bytes: opts.Manifest.SizeBytes, SHA256: digest, Chunks: len(opts.Manifest.Chunks), ReusedChunks: len(opts.Manifest.Chunks)}, nil
	} else if !os.IsNotExist(statErr) {
		return result, statErr
	}
	flags := os.O_CREATE | os.O_EXCL | os.O_RDWR
	if opts.Journal != nil {
		if info, e := os.Lstat(staging); e == nil {
			if !info.Mode().IsRegular() {
				return result, errors.New("暂存路径不是常规文件")
			}
			flags = os.O_RDWR
		} else if !os.IsNotExist(e) {
			return result, e
		}
	}
	file, err := os.OpenFile(staging, flags, 0600)
	if err != nil {
		return result, fmt.Errorf("创建唯一暂存文件: %w", err)
	}
	defer file.Close()
	if flags&os.O_EXCL != 0 {
		needed := uint64(opts.Manifest.SizeBytes)
		if opts.Manifest.PayloadKind == "folder" {
			needed *= 2
		}
		available, e := filesystem.AvailableBytes(filepath.Dir(staging))
		if e != nil {
			return result, e
		}
		if available < needed {
			return result, errors.New("目标磁盘可用空间不足（需保存暂存及目录展开内容）")
		}
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	progress := Progress{TaskID: task.ID, State: "rechecking", TotalBytes: opts.Manifest.SizeBytes}
	emit := func() {
		if opts.OnProgress != nil {
			opts.OnProgress(progress)
		}
	}
	if err = setState("rechecking"); err != nil {
		return result, err
	}
	emit()
	confirmed := make(map[int]bool)
	for _, index := range task.Confirmed {
		if index < 0 || index >= len(opts.Manifest.Chunks) {
			return result, errors.New("已确认分块越界")
		}
		chunk := opts.Manifest.Chunks[index]
		digest, e := manifest.HashReader(ctx, io.NewSectionReader(file, chunk.Offset, chunk.Length))
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		if e == nil && digest == chunk.SHA256 {
			confirmed[index] = true
			progress.VerifiedBytes += chunk.Length
			progress.CompletedChunks++
		} else if err = opts.Journal.SetChunk(ctx, task.ID, index, false); err != nil {
			return result, err
		}
	}
	reused := len(confirmed)
	if err = file.Truncate(opts.Manifest.SizeBytes); err != nil {
		return result, fmt.Errorf("分配目标文件空间: %w", err)
	}
	queue := make(chan v1.Chunk, len(opts.Manifest.Chunks))
	for _, chunk := range opts.Manifest.Chunks {
		if !confirmed[chunk.Index] {
			queue <- chunk
		}
	}
	remaining := len(queue)
	close(queue)
	if err = setState("downloading"); err != nil {
		return result, err
	}
	progress.State = "downloading"
	var mu sync.Mutex
	var wg sync.WaitGroup
	emit()
	for range min(opts.Workers, remaining) {
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
				if err == nil {
					err = boundary("written", chunk.Index)
				}
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
				if err = boundary("synced", chunk.Index); err != nil {
					cancel(err)
					return
				}
				if opts.Journal != nil {
					if err = opts.Journal.SetChunk(ctx, task.ID, chunk.Index, true); err != nil {
						cancel(err)
						return
					}
				}
				if err = boundary("confirmed", chunk.Index); err != nil {
					cancel(err)
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
	if err = setState("verifying"); err != nil {
		return result, err
	}
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
	if err = setState("publishing"); err != nil {
		return result, err
	}
	if err = boundary("publishing", -1); err != nil {
		return result, err
	}
	if opts.Manifest.PayloadKind == "folder" {
		err = folder.Publish(ctx, staging, destination)
	} else {
		err = filesystem.PublishNoReplace(staging, destination)
	}
	if err != nil {
		return result, fmt.Errorf("发布文件失败，目标可能已被创建: %w", err)
	}
	if err = boundary("renamed", -1); err != nil {
		return result, err
	}
	if err = setState("completed"); err != nil {
		return result, err
	}
	if err = boundary("completed", -1); err != nil {
		return result, err
	}
	progress.State = "completed"
	emit()
	return DownloadResult{TaskID: task.ID, ReusedChunks: reused, ManifestID: opts.Manifest.ManifestID, Path: destination, Bytes: opts.Manifest.SizeBytes, SHA256: actual, Chunks: len(opts.Manifest.Chunks)}, nil
}
