package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

func (s *Server) uploadAPI(w http.ResponseWriter, r *http.Request, p *principal) {
	if s.uploadStore == nil {
		fail(w, 404, "not_found", "上传未启用")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path != "/"+strings.Join(parts, "/") {
		fail(w, 404, "not_found", "接口不存在")
		return
	}
	if len(parts) == 2 && parts[0] == "v1" && parts[1] == "uploads" && r.Method == http.MethodPost {
		s.createUpload(w, r, p)
		return
	}
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "uploads" || len(parts[2]) != 32 {
		fail(w, 404, "not_found", "会话不存在")
		return
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		fail(w, 404, "not_found", "会话不存在")
		return
	}
	record, err := s.uploadStore.Upload(r.Context(), parts[2])
	if err != nil || record.OwnerID != p.id || !p.uploadShares[record.ShareID] || !s.writable[record.ShareID] {
		fail(w, 404, "not_found", "会话不存在或未授权")
		return
	}
	root := s.roots[record.ShareID]
	value, _ := s.uploadLocks.LoadOrStore(record.ID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	lock, err := filesystem.Lock(filepath.Join(root.Name(), ".hopsend-uploads", record.ID, "session.lock"))
	if err != nil {
		fail(w, 409, "upload_busy", "会话正在处理，请稍后重试")
		return
	}
	defer lock.Close()
	// Refresh after taking ownership; another handler may have finalized it.
	record, err = s.uploadStore.Upload(r.Context(), record.ID)
	if err != nil {
		fail(w, 500, "state_unavailable", "会话状态不可用")
		return
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute)); err == nil {
			record, err = s.recheckUpload(r.Context(), record, root)
		}
	case len(parts) == 6 && parts[3] == "chunks" && parts[5] == "confirm" && r.Method == http.MethodPost:
		var index int
		index, err = strconv.Atoi(parts[4])
		if err == nil {
			record, err = s.confirmUpload(r.Context(), record, root, index)
		}
	case len(parts) == 4 && parts[3] == "finalize" && r.Method == http.MethodPost:
		// File bytes still never travel over HTTPS. This extends only the local
		// full-file validation/publication response, bounded to ten minutes.
		if err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute)); err == nil {
			record, err = s.finalizeUpload(r.Context(), record, root)
		}
	default:
		fail(w, 405, "method_not_allowed", "会话操作或方法不支持")
		return
	}
	if err != nil {
		fail(w, 409, "upload_not_confirmed", err.Error())
		return
	}
	send(w, 200, record.UploadSession)
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request, p *principal) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, manifest.MaxJSONBytes))
	decoder.DisallowUnknownFields()
	var request v1.CreateUpload
	if err := decoder.Decode(&request); err != nil {
		fail(w, 400, "invalid_upload", "上传请求格式错误")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_upload", "请求包含额外内容")
		return
	}
	if !manifest.ValidID(request.RequestID) || manifest.Validate(request.Manifest) != nil || request.ShareID != request.Manifest.ShareID || filesystem.SafeLeaf(request.Target) != nil {
		fail(w, 400, "invalid_upload", "清单、请求 ID 或目标文件名无效")
		return
	}
	root := s.roots[request.ShareID]
	if root == nil || !s.writable[request.ShareID] || !p.uploadShares[request.ShareID] {
		fail(w, 403, "upload_denied", "未获此共享的上传授权")
		return
	}
	s.uploadCreateMu.Lock()
	defer s.uploadCreateMu.Unlock()
	existing, err := s.uploadStore.FindUpload(r.Context(), p.id, request.RequestID)
	if err == nil {
		if existing.ManifestID != request.Manifest.ManifestID || existing.ShareID != request.ShareID || existing.Target != request.Target {
			fail(w, 409, "request_conflict", "请求 ID 已绑定其他内容")
			return
		}
		if existing.State != "completed" && existing.State != "publishing" {
			if err = s.ensureUploadFile(existing, root); err != nil {
				fail(w, 500, "staging_unavailable", "无法准备暂存文件")
				return
			}
		}
		send(w, 200, existing.UploadSession)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 500, "state_unavailable", "会话状态不可用")
		return
	}
	if _, err = root.Lstat(request.Target); !os.IsNotExist(err) {
		fail(w, 409, "target_exists", "目标已存在或不可访问，不会覆盖")
		return
	}
	available, err := filesystem.AvailableBytes(root.Name())
	if err != nil || available < uint64(request.Manifest.SizeBytes) {
		fail(w, 507, "insufficient_storage", "接收端可用空间不足或无法确认容量")
		return
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		fail(w, 500, "session_unavailable", "无法生成会话")
		return
	}
	id := hex.EncodeToString(random)
	staging := filepath.Join(".hopsend-uploads", id, "data.partial")
	sftpPath := filepath.ToSlash(filepath.Join(root.Name(), staging))
	if !strings.HasPrefix(sftpPath, "/") {
		sftpPath = "/" + sftpPath
	}
	record, err := s.uploadStore.CreateUpload(r.Context(), p.id, request, id, staging, sftpPath)
	if err != nil {
		fail(w, 500, "state_unavailable", "无法保存会话")
		return
	}
	if err = s.ensureUploadFile(record, root); err != nil {
		code := 507
		if os.IsPermission(err) {
			code = 403
		}
		fail(w, code, "staging_unavailable", "无法分配接收端暂存文件，请检查磁盘容量和 SSH/Agent 文件权限")
		return
	}
	send(w, 201, record.UploadSession)
}

func (s *Server) ensureUploadFile(r sqlite.UploadRecord, root *os.Root) error {
	if err := root.MkdirAll(filepath.Dir(r.Staging), 0700); err != nil {
		return err
	}
	if info, err := root.Lstat(r.Staging); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("暂存路径不是常规文件")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(r.Staging, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Truncate(r.Manifest.SizeBytes); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Server) reconcilePublished(ctx context.Context, r sqlite.UploadRecord, root *os.Root) (sqlite.UploadRecord, bool, error) {
	if r.State != "completed" && r.State != "publishing" {
		return r, false, nil
	}
	info, err := root.Lstat(r.Target)
	if os.IsNotExist(err) && r.State == "publishing" {
		return r, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != r.Manifest.SizeBytes {
		return r, false, errors.New("接收端正式文件缺失、变化或类型错误")
	}
	staged, stageErr := root.Lstat(r.Staging)
	if stageErr == nil && !os.SameFile(info, staged) {
		return r, false, errors.New("同名正式文件与暂存不同，不会覆盖或冒认")
	}
	if stageErr != nil && !os.IsNotExist(stageErr) {
		return r, false, errors.New("无法确认发布边界")
	}
	f, err := root.Open(r.Target)
	if err != nil {
		return r, false, errors.New("正式文件不可读取")
	}
	digest, err := manifest.HashReader(ctx, f)
	_ = f.Close()
	if err != nil || digest != r.Manifest.ContentID {
		return r, false, errors.New("接收端正式文件完整哈希变化")
	}
	if stageErr == nil {
		if err = root.Remove(r.Staging); err != nil {
			return r, false, errors.New("接收端仍未释放暂存文件")
		}
	}
	if err = s.uploadStore.SetUploadState(ctx, r.ID, "completed"); err != nil {
		return r, false, err
	}
	r.State, r.SHA256 = "completed", digest
	return r, true, nil
}

func (s *Server) recheckUpload(ctx context.Context, r sqlite.UploadRecord, root *os.Root) (sqlite.UploadRecord, error) {
	if record, done, err := s.reconcilePublished(ctx, r, root); done || err != nil {
		return record, err
	}
	if err := s.ensureUploadFile(r, root); err != nil {
		return r, errors.New("接收端暂存文件不可用")
	}
	f, err := root.OpenFile(r.Staging, os.O_RDWR, 0600)
	if err != nil {
		return r, errors.New("接收端暂存文件不可读")
	}
	defer f.Close()
	valid := make([]int, 0, len(r.Confirmed))
	for _, i := range r.Confirmed {
		chunk := r.Manifest.Chunks[i]
		digest, e := manifest.HashReader(ctx, io.NewSectionReader(f, chunk.Offset, chunk.Length))
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if e == nil && digest == chunk.SHA256 {
			valid = append(valid, i)
		} else if err = s.uploadStore.SetUploadChunk(ctx, r.ID, i, false); err != nil {
			return r, err
		}
	}
	if err = f.Truncate(r.Manifest.SizeBytes); err != nil {
		return r, errors.New("无法恢复暂存尺寸")
	}
	r.Confirmed = valid
	r.State = "uploading"
	if err = s.uploadStore.SetUploadState(ctx, r.ID, r.State); err != nil {
		return r, err
	}
	return r, nil
}

func (s *Server) confirmUpload(ctx context.Context, r sqlite.UploadRecord, root *os.Root, index int) (sqlite.UploadRecord, error) {
	if r.State == "completed" {
		return r, nil
	}
	if r.State != "uploading" || index < 0 || index >= len(r.Manifest.Chunks) {
		return r, errors.New("分块索引或会话状态无效")
	}
	info, err := root.Lstat(r.Staging)
	if err != nil || !info.Mode().IsRegular() {
		return r, errors.New("暂存文件不可访问")
	}
	f, err := root.OpenFile(r.Staging, os.O_RDWR, 0600)
	if err != nil {
		return r, errors.New("接收端无法读取分块")
	}
	defer f.Close()
	chunk := r.Manifest.Chunks[index]
	digest, err := manifest.HashReader(ctx, io.NewSectionReader(f, chunk.Offset, chunk.Length))
	if err != nil || digest != chunk.SHA256 {
		if e := s.uploadStore.SetUploadChunk(ctx, r.ID, index, false); e != nil {
			return r, e
		}
		return r, errors.New("接收端分块 SHA-256 不一致，未确认")
	}
	if err = f.Sync(); err != nil {
		return r, errors.New("接收端文件同步失败，未确认")
	}
	if err = s.uploadStore.SetUploadChunk(ctx, r.ID, index, true); err != nil {
		return r, errors.New("接收端确认事务失败")
	}
	return s.uploadStore.Upload(ctx, r.ID)
}

func (s *Server) finalizeUpload(ctx context.Context, r sqlite.UploadRecord, root *os.Root) (sqlite.UploadRecord, error) {
	r, err := s.recheckUpload(ctx, r, root)
	if err != nil || r.State == "completed" {
		return r, err
	}
	if len(r.Confirmed) != len(r.Manifest.Chunks) {
		return r, errors.New("仍有未确认或损坏分块，不能发布")
	}
	if err = s.uploadStore.SetUploadState(ctx, r.ID, "verifying"); err != nil {
		return r, err
	}
	f, err := root.OpenFile(r.Staging, os.O_RDWR, 0600)
	if err != nil {
		return r, errors.New("接收端暂存文件不可读")
	}
	digest, hashErr := manifest.HashReader(ctx, f)
	syncErr := f.Sync()
	closeErr := f.Close()
	if hashErr != nil || syncErr != nil || closeErr != nil || digest != r.Manifest.ContentID {
		_ = s.uploadStore.SetUploadState(context.WithoutCancel(ctx), r.ID, "uploading")
		return r, errors.New("接收端全文件校验或同步未通过，未发布")
	}
	if err = s.uploadStore.SetUploadState(ctx, r.ID, "publishing"); err != nil {
		return r, err
	}
	// Root.Link atomically refuses an existing target and confines resolution to
	// the authorized share even if a directory entry is replaced concurrently.
	if err = root.Link(r.Staging, r.Target); err != nil {
		return r, fmt.Errorf("接收端禁止覆盖发布失败: %w", errors.New("目标已存在或文件系统不支持安全发布"))
	}
	if err = root.Remove(r.Staging); err != nil {
		return r, errors.New("文件已建立发布链接，等待暂存释放后对账")
	}
	if err = s.uploadStore.SetUploadState(ctx, r.ID, "completed"); err != nil {
		return r, err
	}
	r.State, r.SHA256 = "completed", digest
	return r, nil
}
