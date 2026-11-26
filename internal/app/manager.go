package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

var ErrPaused = errors.New("任务已暂停")
var ErrCancelled = errors.New("任务已取消")
var ErrUploadBudget = errors.New("上传活动块预算已满，等待其他任务释放")

type TaskStatus struct {
	engine.Progress
	Error               string  `json:"error,omitempty"`
	TransferredBytes    int64   `json:"transferred_bytes"`
	SpeedBytesPerSecond float64 `json:"speed_bytes_per_second"`
	ETASeconds          float64 `json:"eta_seconds"`
	MetricsAvailable    bool    `json:"metrics_available"`
	UpdatedAt           string  `json:"updated_at"`
}

type execution struct {
	meter         *transferMeter
	cancel        context.CancelCauseFunc
	done          chan struct{}
	result        engine.DownloadResult
	err           error
	uploadNode    string
	uploadWorkers int
}

// Manager owns live task lifetimes. A stop completes only after the Download
// call has joined its workers and released the destination ownership handle.
type Manager struct {
	events   *eventLog
	database string
	mu       sync.Mutex
	active   map[string]*execution
	latest   map[string]TaskStatus
	finished map[string]*execution
	changed  func(TaskStatus)
	run      func(context.Context, DownloadRequest) (engine.DownloadResult, error)
}

func NewManager(database string, changed func(TaskStatus)) *Manager {
	if database == "" {
		database = DefaultDatabase()
	}
	return &Manager{events: newEventLog(database), database: database, active: make(map[string]*execution), latest: make(map[string]TaskStatus), finished: make(map[string]*execution), changed: changed, run: Transfer}
}

func (m *Manager) update(status TaskStatus) {
	status.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	m.mu.Lock()
	e := m.active[status.TaskID]
	if e == nil {
		e = m.finished[status.TaskID]
	}
	if e != nil {
		e.meter.progress(status)
		status = e.meter.decorate(status)
	}
	m.latest[status.TaskID] = status
	changed := m.changed
	m.mu.Unlock()
	m.events.append(status)
	if changed != nil {
		changed(status)
	}
}

func (m *Manager) Status(id string) (TaskStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.latest[id]
	e := m.active[id]
	if e == nil {
		e = m.finished[id]
	}
	if e != nil {
		s = e.meter.decorate(s)
	}
	return s, ok
}

func (m *Manager) Start(parent context.Context, r DownloadRequest) (string, error) {
	if !manifest.ValidID(r.ManifestID) || r.Destination == "" || r.Workers < 1 || r.Workers > 8 {
		return "", errors.New("任务版本、目标或并发参数无效")
	}
	destination := r.Destination
	if r.Direction != "upload" {
		var err error
		destination, err = filepath.Abs(r.Destination)
		if err != nil {
			return "", err
		}
	}
	r.Destination, r.Database = destination, m.database
	id := engine.TaskID(r.ManifestID, destination)
	ctx, cancel := context.WithCancelCause(parent)
	e := &execution{meter: newTransferMeter(), cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	if _, ok := m.active[id]; ok {
		m.mu.Unlock()
		cancel(nil)
		return "", errors.New("任务正在运行")
	}
	if r.Direction == "upload" {
		global, node := 0, 0
		for _, active := range m.active {
			global += active.uploadWorkers
			if active.uploadNode == r.SSH.Address {
				node += active.uploadWorkers
			}
		}
		if r.Workers > 2 || global+r.Workers > 4 || node+r.Workers > 2 {
			m.mu.Unlock()
			cancel(nil)
			return "", ErrUploadBudget
		}
		e.uploadNode = r.SSH.Address
		e.uploadWorkers = r.Workers
	}
	m.active[id] = e
	delete(m.finished, id)
	m.mu.Unlock()
	m.update(TaskStatus{Progress: engine.Progress{TaskID: id, State: "connecting"}})
	originalBytes := r.OnBytes
	r.OnBytes = func(n int64) {
		e.meter.add(n)
		if originalBytes != nil {
			originalBytes(n)
		}
	}
	original := r.OnProgress
	r.OnProgress = func(p engine.Progress) {
		m.update(TaskStatus{Progress: p})
		if original != nil {
			original(p)
		}
	}
	go func() {
		defer cancel(nil)
		e.result, e.err = m.run(ctx, r)
		status, _ := m.Status(id)
		status.State = "completed"
		if e.err != nil {
			status.State = "failed"
			status.Error = e.err.Error()
			if ctx.Err() != nil {
				status.State = "paused"
				e.err = ErrPaused
				if errors.Is(context.Cause(ctx), ErrCancelled) {
					status.State = "cancelled"
					e.err = ErrCancelled
				}
				status.Error = ""
			}
		}
		// Publication may already have happened when a stop races completion.
		// A successful engine result always remains completed.
		if err := setExistingTaskState(context.WithoutCancel(parent), m.database, id, status.State); err != nil {
			e.err = errors.Join(e.err, err)
			status.State = "failed"
			status.Error = e.err.Error()
		}
		m.update(status)
		m.mu.Lock()
		delete(m.active, id)
		m.finished[id] = e
		close(e.done)
		m.mu.Unlock()
	}()
	return id, nil
}

func (m *Manager) Wait(ctx context.Context, id string) (engine.DownloadResult, error) {
	m.mu.Lock()
	e := m.active[id]
	if e == nil {
		e = m.finished[id]
	}
	m.mu.Unlock()
	if e == nil {
		return engine.DownloadResult{}, errors.New("任务未在本进程执行")
	}
	select {
	case <-e.done:
		return e.result, e.err
	case <-ctx.Done():
		return engine.DownloadResult{}, ctx.Err()
	}
}

func (m *Manager) stop(ctx context.Context, id string, cause error) error {
	m.mu.Lock()
	e := m.active[id]
	finished := m.finished[id]
	m.mu.Unlock()
	if e == nil {
		if finished != nil {
			return nil
		}
		return errors.New("任务未在本进程运行；请从任务列表继续或检查其他进程")
	}
	e.cancel(cause)
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) Pause(ctx context.Context, id string) error  { return m.stop(ctx, id, ErrPaused) }
func (m *Manager) Cancel(ctx context.Context, id string) error { return m.stop(ctx, id, ErrCancelled) }

func (m *Manager) Resume(ctx context.Context, id string) (string, error) {
	store, err := sqlite.Open(m.database)
	if err != nil {
		return "", err
	}
	task, err := store.Get(ctx, id)
	_ = store.Close()
	if err != nil {
		return "", err
	}
	if task.State == "cancelled" {
		return "", errors.New("已取消任务不能直接继续；需要明确创建新下载")
	}
	var request DownloadRequest
	if err = json.Unmarshal(task.Source, &request); err != nil {
		return "", err
	}
	if request.ManifestID != task.Manifest.ManifestID || request.Destination != task.Destination {
		return "", errors.New("恢复配置与任务身份不一致")
	}
	return m.Start(ctx, request)
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	running := make([]*execution, 0, len(m.active))
	for _, e := range m.active {
		e.cancel(ErrPaused)
		running = append(running, e)
	}
	m.mu.Unlock()
	for _, e := range running {
		select {
		case <-e.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) Logs() ([]TaskStatus, error) { return m.events.read() }
