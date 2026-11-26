package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/folder"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

type BatchTarget struct {
	DeviceID   string          `json:"device_id"`
	DeviceName string          `json:"device_name"`
	Request    DownloadRequest `json:"-"`
}

// BatchUploads owns the finite preparation and dispatch queues. It reuses the
// existing task manager; each receiver still confirms its independent session.
type BatchUploads struct {
	mu            sync.Mutex
	controlMu     sync.Mutex
	store         *sqlite.Store
	manager       *Manager
	database      string
	active        map[string]chan struct{}
	cancel        context.CancelFunc
	done          chan struct{}
	owner         *os.File
	closed        bool
	preparing     bool
	prepareCancel context.CancelFunc
	prepareDone   chan struct{}
	released      bool
	lastError     error
}

func NewBatchUploads(ctx context.Context, database string, manager *Manager) (*BatchUploads, error) {
	if err := os.MkdirAll(filepath.Dir(database), 0700); err != nil {
		return nil, err
	}
	owner, err := filesystem.Lock(filepath.Join(filepath.Dir(database), "batch-owner.lock"))
	if err != nil {
		return nil, errors.New("批次队列由另一个桌面进程使用")
	}
	store, err := sqlite.Open(database)
	if err != nil {
		owner.Close()
		return nil, err
	}
	if err := store.RecoverBatchQueue(ctx); err != nil {
		store.Close()
		owner.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	p := &BatchUploads{store: store, manager: manager, database: database, active: map[string]chan struct{}{}, cancel: cancel, done: make(chan struct{}), owner: owner}
	go p.loop(lifetime)
	return p, nil
}

func (p *BatchUploads) loop(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.schedule(ctx)
		}
	}
}

func (p *BatchUploads) schedule(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.active) >= 2 {
		return
	}
	items, err := p.store.QueuedBatchRecipients(ctx)
	if err != nil {
		p.lastError = err
		return
	}
	for _, item := range items {
		if len(p.active) >= 2 {
			break
		}
		if _, ok := p.active[item.TaskID]; ok {
			continue
		}
		var r DownloadRequest
		batch, batchErr := p.store.Batch(ctx, item.BatchID)
		if err := json.Unmarshal(item.Request, &r); err != nil || batchErr != nil || r.BatchID != item.BatchID || r.ManifestID != batch.Manifest.ManifestID || r.SnapshotDirectory != batch.SnapshotDirectory || r.Database != p.database || r.Direction != "upload" || r.UploadManifestScope != "source_snapshot" || engine.TaskID(r.ManifestID, r.Destination) != item.TaskID {
			p.lastError = p.store.SetBatchRecipient(ctx, item.TaskID, "failed", "批次恢复配置损坏")
			continue
		}
		if err := p.store.SetBatchRecipient(ctx, item.TaskID, "running", ""); err != nil {
			p.lastError = err
			return
		}
		// Stopping the scheduler must not race explicit cancellation of writers.
		// Close joins them through Manager.Pause before releasing queue ownership.
		id, err := p.manager.Start(context.WithoutCancel(ctx), r)
		if err != nil {
			state := "failed"
			message := err.Error()
			if errors.Is(err, ErrUploadBudget) {
				state = "queued"
				message = ""
			}
			p.lastError = p.store.SetBatchRecipient(ctx, item.TaskID, state, message)
			continue
		}
		done := make(chan struct{})
		p.active[id] = done
		go p.wait(id, done)
	}
}

func (p *BatchUploads) wait(id string, done chan struct{}) {
	_, err := p.manager.Wait(context.Background(), id)
	status, _ := p.manager.Status(id)
	state := status.State
	if state != "completed" && state != "cancelled" && state != "paused" {
		state = "failed"
	}
	message := ""
	if state == "failed" && err != nil {
		message = err.Error()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.store.SetBatchRecipient(context.Background(), id, state, message); err != nil {
		p.lastError = err
	}
	delete(p.active, id)
	close(done)
}

func estimateBatchSnapshot(ctx context.Context, source, store string) (uint64, error) {
	if err := filesystem.CheckComponents(source); err != nil {
		return 0, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return 0, err
	}
	if filesystem.IsReparse(info) {
		return 0, errors.New("源内容不能是链接")
	}
	const maximum uint64 = 64 << 30
	var bytes uint64
	if info.Mode().IsRegular() {
		bytes = uint64(info.Size())
		if bytes > maximum {
			return 0, errors.New("批次源文件超过64GiB准备上限")
		}
	} else if info.IsDir() {
		rel, err := filepath.Rel(source, store)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return 0, errors.New("本机快照库不能位于待发送目录内")
		}
		root, err := os.OpenRoot(source)
		if err != nil {
			return 0, err
		}
		defer root.Close()
		count := 0
		err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == "." {
				return nil
			}
			count++
			if count > folder.MaxEntries {
				return errors.New("目录项超过上限")
			}
			info, err := root.Lstat(path)
			if err != nil {
				return err
			}
			if filesystem.IsReparse(info) {
				return errors.New("目录中包含链接")
			}
			bytes += 16384
			if info.Mode().IsRegular() {
				bytes += uint64(info.Size())
			} else if !info.IsDir() {
				return errors.New("目录中有非常规文件")
			}
			if bytes > maximum {
				return errors.New("批次源内容超过64GiB准备上限")
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		bytes *= 2 // Directory package and immutable object coexist during preparation.
	} else {
		return 0, errors.New("请选择常规文件或文件夹")
	}
	if bytes > 2*maximum {
		return 0, errors.New("批次源内容超过64GiB准备上限")
	}
	return bytes + (64 << 20), nil
}

var batchKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (p *BatchUploads) Create(ctx context.Context, key, source string, targets []BatchTarget) (sqlite.UploadBatch, error) {
	var zero sqlite.UploadBatch
	if !batchKeyPattern.MatchString(key) || len(targets) < 1 || len(targets) > 32 {
		return zero, errors.New("一次批次须含1～32个目标和有效请求键")
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return zero, err
	}
	name := filepath.Base(absolute)
	if filesystem.SafeLeaf(name) != nil {
		return zero, errors.New("源文件名不可移植")
	}
	seen := map[string]bool{}
	for i := range targets {
		r := &targets[i].Request
		parent, err := filesystem.UploadParent(r.UploadParentPath)
		if err != nil {
			return zero, err
		}
		r.UploadParentPath = parent
		if r.UploadTarget == "" {
			r.UploadTarget = name
		}
		if filesystem.SafeLeaf(r.UploadTarget) != nil {
			return zero, errors.New("接收文件名无效")
		}
		if targets[i].DeviceID == "" || r.Agent.URL == "" || r.ShareID == "" {
			return zero, errors.New("接收设备配置不完整")
		}
		r.Destination = UploadDestination(r.Agent.URL, r.ShareID, r.UploadTarget, parent)
		if seen[r.Destination] {
			return zero, errors.New("同一目标重复选择，未创建批次")
		}
		seen[r.Destination] = true
		r.Workers = 2
		r.BatchID = key
		r.UploadManifestScope = "source_snapshot"
		r.Direction = "upload"
	}
	// Include fixed trust references and every target. A reused key may not turn
	// into a different send, and duplicate submission never republishes bytes.
	intentItems := []any{absolute}
	for _, target := range targets {
		intentItems = append(intentItems, target.DeviceID, target.Request)
	}
	intentBytes, err := json.Marshal(intentItems)
	if err != nil {
		return zero, err
	}
	digest := sha256.Sum256(intentBytes)
	intent := hex.EncodeToString(digest[:])
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return zero, errors.New("批次队列已关闭")
	}
	if old, err := p.store.Batch(ctx, key); err == nil {
		p.mu.Unlock()
		if old.Intent != intent {
			return zero, errors.New("请求键已绑定其他内容或目标")
		}
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		p.mu.Unlock()
		return zero, err
	}
	if p.preparing {
		p.mu.Unlock()
		return zero, errors.New("已有批次快照正在准备，请稍后重试")
	}
	all, err := p.store.Batches(ctx)
	if err != nil {
		p.mu.Unlock()
		return zero, err
	}
	open := 0
	for _, b := range all {
		for _, item := range b.Recipients {
			if item.SnapshotHeld {
				open++
				break
			}
		}
	}
	if open >= 16 {
		p.mu.Unlock()
		return zero, errors.New("最多保留16个未结束批次，请先完成或取消旧批次")
	}
	prepareCtx, prepareCancel := context.WithCancel(ctx)
	prepareDone := make(chan struct{})
	p.preparing = true
	p.prepareCancel = prepareCancel
	p.prepareDone = prepareDone
	p.mu.Unlock()
	defer func() {
		prepareCancel()
		p.mu.Lock()
		p.preparing = false
		p.prepareCancel = nil
		close(prepareDone)
		p.mu.Unlock()
	}()
	ctx = prepareCtx
	lock, err := filesystem.Lock(filepath.Join(filepath.Dir(p.database), "batch-prepare.lock"))
	if err != nil {
		return zero, errors.New("本机快照准备正忙")
	}
	defer lock.Close()
	store := filepath.Join(filepath.Dir(p.database), "batch-content")
	needed, err := estimateBatchSnapshot(ctx, absolute, store)
	if err != nil {
		return zero, err
	}
	available, err := filesystem.AvailableBytes(filepath.Dir(p.database))
	if err != nil || available < needed {
		return zero, errors.New("本机磁盘不足以准备本批次快照")
	}
	m, err := Publish(ctx, absolute, store, "source-snapshot", manifest.DefaultChunkSize)
	if err != nil {
		return zero, err
	}
	b := sqlite.UploadBatch{ID: key, Intent: intent, Name: name, Manifest: m, SnapshotDirectory: store}
	for _, target := range targets {
		r := target.Request
		r.SnapshotDirectory = store
		r.ManifestID = m.ManifestID
		r.Database = p.database
		raw, err := json.Marshal(r)
		if err != nil {
			return zero, err
		}
		b.Recipients = append(b.Recipients, sqlite.BatchRecipient{TaskID: engine.TaskID(m.ManifestID, r.Destination), BatchID: key, DeviceID: target.DeviceID, DeviceName: target.DeviceName, Request: raw})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return zero, errors.New("批次队列关闭，未启动发送")
	}
	if err := p.store.CreateBatch(ctx, b); err != nil {
		return zero, err
	}
	return p.store.Batch(ctx, key)
}

func (p *BatchUploads) List(ctx context.Context) ([]sqlite.UploadBatch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastError != nil {
		return nil, p.lastError
	}
	return p.store.Batches(ctx)
}

func (p *BatchUploads) Control(ctx context.Context, batchID, taskID, action string) error {
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	if action != "pause" && action != "resume" && action != "cancel" {
		return errors.New("批次操作无效")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("批次队列已关闭")
	}
	b, err := p.store.Batch(ctx, batchID)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	if taskID == "" {
		mode := map[string]string{"pause": "paused", "resume": "running", "cancel": "cancelled"}[action]
		if err := p.store.SetBatchMode(ctx, b.ID, mode); err != nil {
			p.mu.Unlock()
			return err
		}
	}
	type waiting struct {
		id   string
		done chan struct{}
	}
	waits := []waiting{}
	matched := false
	for _, item := range b.Recipients {
		if taskID != "" && item.TaskID != taskID {
			continue
		}
		matched = true
		if item.State == "completed" || item.State == "cancelled" {
			continue
		}
		if done, active := p.active[item.TaskID]; active {
			if action != "resume" {
				waits = append(waits, waiting{item.TaskID, done})
			}
			continue
		}
		state := map[string]string{"pause": "paused", "resume": "queued", "cancel": "cancelled"}[action]
		if err := p.store.SetBatchRecipient(ctx, item.TaskID, state, ""); err != nil {
			p.mu.Unlock()
			return err
		}
	}
	if taskID != "" && action == "resume" {
		err = p.store.SetBatchMode(ctx, b.ID, "running")
	}
	p.mu.Unlock()
	if !matched {
		return errors.New("批次中没有此任务")
	}
	if err != nil {
		return err
	}
	for _, w := range waits {
		var err error
		if action == "cancel" {
			err = p.manager.Cancel(ctx, w.id)
		} else {
			err = p.manager.Pause(ctx, w.id)
		}
		if err != nil {
			return err
		}
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *BatchUploads) ControlTask(ctx context.Context, id, action string) (bool, error) {
	batch, err := p.store.BatchForTask(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, p.Control(ctx, batch, id, action)
}

func (p *BatchUploads) Close(ctx context.Context) error {
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cancel()
	if p.prepareCancel != nil {
		p.prepareCancel()
	}
	preparing := p.preparing
	prepareDone := p.prepareDone
	p.mu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if preparing {
		select {
		case <-prepareDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	waits := map[string]chan struct{}{}
	for id, done := range p.active {
		waits[id] = done
	}
	p.mu.Unlock()
	for id, done := range waits {
		if err := p.manager.Pause(ctx, id); err != nil {
			return err
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.store.RecoverBatchQueue(ctx); err != nil {
		return err
	}
	if err := p.store.Close(); err != nil {
		return err
	}
	p.released = true
	return p.owner.Close()
}
