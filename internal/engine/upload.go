package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type RangeSink interface {
	WriteRange(context.Context, string, int64, int64, io.Reader) (int64, error)
	Close() error
}
type SinkFactory func(context.Context) (RangeSink, error)
type UploadReceiver interface {
	UploadStatus(context.Context, string) (v1.UploadSession, error)
	ConfirmUpload(context.Context, string, int) (v1.UploadSession, error)
	FinalizeUpload(context.Context, string) (v1.UploadSession, error)
}
type UploadOptions struct {
	Manifest    manifestv1.Manifest
	Snapshot    string
	Destination string
	LockPath    string
	Session     v1.UploadSession
	Workers     int
	OpenSink    SinkFactory
	Receiver    UploadReceiver
	Journal     Journal
	Source      json.RawMessage
	OnProgress  func(Progress)
}

func validateUploadSession(m manifestv1.Manifest, expected, actual v1.UploadSession) error {
	if actual.ID != expected.ID || actual.RequestID != expected.RequestID || actual.Target != expected.Target || actual.ParentPath != expected.ParentPath || actual.ShareID != m.ShareID || actual.ManifestID != m.ManifestID || actual.SFTPPath != expected.SFTPPath {
		return errors.New("上传会话身份或内容版本变化")
	}
	seen := map[int]bool{}
	for _, i := range actual.Confirmed {
		if i < 0 || i >= len(m.Chunks) || seen[i] {
			return errors.New("接收端确认块索引重复或越界")
		}
		seen[i] = true
	}
	return nil
}

func Upload(parent context.Context, o UploadOptions) (result DownloadResult, returnedErr error) {
	if err := manifest.Validate(o.Manifest); err != nil {
		return result, err
	}
	if o.Workers < 1 || o.Workers > 8 || o.OpenSink == nil || o.Receiver == nil || o.Journal == nil || o.LockPath == "" || o.Destination == "" {
		return result, errors.New("上传参数无效")
	}
	lock, err := filesystem.Lock(o.LockPath)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	task := Task{ID: TaskID(o.Manifest.ManifestID, o.Destination), Manifest: o.Manifest, Destination: o.Destination, RemotePath: o.Session.SFTPPath, Staging: o.Snapshot, Source: o.Source}
	if _, err = o.Journal.Prepare(parent, task); err != nil {
		return result, err
	}
	setState := func(state string) error { return o.Journal.SetState(context.WithoutCancel(parent), task.ID, state) }
	defer func() {
		if returnedErr != nil {
			returnedErr = errors.Join(returnedErr, setState("interrupted"))
		}
	}()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	progress := Progress{TaskID: task.ID, State: "rechecking", TotalBytes: o.Manifest.SizeBytes}
	emit := func() {
		if o.OnProgress != nil {
			o.OnProgress(progress)
		}
	}
	if err = setState("rechecking"); err != nil {
		return result, err
	}
	emit()
	session, err := o.Receiver.UploadStatus(ctx, o.Session.ID)
	if err != nil {
		return result, err
	}
	if err = validateUploadSession(o.Manifest, o.Session, session); err != nil {
		return result, err
	}
	confirmed := map[int]bool{}
	for _, i := range session.Confirmed {
		confirmed[i] = true
		progress.VerifiedBytes += o.Manifest.Chunks[i].Length
		progress.CompletedChunks++
	}
	// The receiver's revalidated confirmations are authoritative after a lost ACK.
	for _, chunk := range o.Manifest.Chunks {
		if err = o.Journal.SetChunk(ctx, task.ID, chunk.Index, confirmed[chunk.Index]); err != nil {
			return result, err
		}
	}
	reused := len(confirmed)
	if session.State != "completed" {
		f, err := os.Open(o.Snapshot)
		if err != nil {
			return result, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != o.Manifest.SizeBytes {
			return result, errors.New("本机上传快照不可用或尺寸变化")
		}
		queue := make(chan manifestv1.Chunk, len(o.Manifest.Chunks))
		for _, chunk := range o.Manifest.Chunks {
			if !confirmed[chunk.Index] {
				queue <- chunk
			}
		}
		remaining := len(queue)
		close(queue)
		if err = setState("uploading"); err != nil {
			return result, err
		}
		progress.State = "uploading"
		emit()
		var mu sync.Mutex
		var wg sync.WaitGroup
		for range min(o.Workers, remaining) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sink, err := o.OpenSink(ctx)
				if err != nil {
					cancel(err)
					return
				}
				defer sink.Close()
				for chunk := range queue {
					if ctx.Err() != nil {
						return
					}
					h := sha256.New()
					reader := io.TeeReader(io.NewSectionReader(f, chunk.Offset, chunk.Length), h)
					n, err := sink.WriteRange(ctx, session.SFTPPath, chunk.Offset, chunk.Length, reader)
					if err != nil {
						cancel(fmt.Errorf("写入上传块 %d: %w", chunk.Index, err))
						return
					}
					if n != chunk.Length || hex.EncodeToString(h.Sum(nil)) != chunk.SHA256 {
						cancel(errors.New("本机上传块长度或哈希变化"))
						return
					}
					ack, err := o.Receiver.ConfirmUpload(ctx, session.ID, chunk.Index)
					if err != nil {
						cancel(err)
						return
					}
					if err = validateUploadSession(o.Manifest, session, ack); err != nil {
						cancel(err)
						return
					}
					acknowledged := false
					for _, i := range ack.Confirmed {
						if i == chunk.Index {
							acknowledged = true
							break
						}
					}
					if !acknowledged {
						cancel(errors.New("接收端未确认此分块"))
						return
					}
					if err = o.Journal.SetChunk(ctx, task.ID, chunk.Index, true); err != nil {
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
		if err = setState("verifying"); err != nil {
			return result, err
		}
		progress.State = "verifying"
		emit()
		session, err = o.Receiver.FinalizeUpload(ctx, session.ID)
		if err != nil {
			return result, err
		}
		if err = validateUploadSession(o.Manifest, o.Session, session); err != nil {
			return result, err
		}
	}
	if session.State != "completed" || session.SHA256 != o.Manifest.ContentID {
		return result, errors.New("接收端尚未完成完整校验和发布")
	}
	if err = setState("completed"); err != nil {
		return result, err
	}
	progress.State = "completed"
	progress.VerifiedBytes = o.Manifest.SizeBytes
	progress.CompletedChunks = len(o.Manifest.Chunks)
	emit()
	return DownloadResult{TaskID: task.ID, ManifestID: o.Manifest.ManifestID, Path: o.Destination, Bytes: o.Manifest.SizeBytes, SHA256: session.SHA256, Chunks: len(o.Manifest.Chunks), ReusedChunks: reused}, nil
}
