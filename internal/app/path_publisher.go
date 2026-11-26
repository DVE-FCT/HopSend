package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/agent/httpapi"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	"github.com/DVE-FCT/HopSend/internal/publication"
)

type PublicationShare struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
type PublicationConfig struct {
	Revision string             `json:"revision"`
	Shares   []PublicationShare `json:"shares"`
}
type PublicationJob struct {
	ID      string          `json:"id"`
	State   string          `json:"state"`
	Path    string          `json:"path"`
	Error   string          `json:"error,omitempty"`
	Version *v1.PathVersion `json:"version,omitempty"`
}

func ReadPublicationConfig(path string) (PublicationConfig, error) {
	result, _, err := readPublicationConfig(path)
	return result, err
}
func readPublicationConfig(path string) (PublicationConfig, httpapi.Config, error) {
	var result PublicationConfig
	cfg, err := httpapi.LoadConfig(path)
	if err != nil {
		return result, cfg, err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return result, cfg, err
	}
	digest := sha256.Sum256(data)
	result.Revision = hex.EncodeToString(digest[:])
	if cfg.ContentDirectory == "" {
		return result, cfg, errors.New("Agent 配置缺少内容库，请先完成源设备准备")
	}
	seen := map[string]bool{}
	for _, s := range cfg.Shares {
		if seen[s.ID] {
			return result, cfg, errors.New("共享 ID 重复")
		}
		seen[s.ID] = true
		result.Shares = append(result.Shares, PublicationShare{s.ID, s.Path})
	}
	if len(result.Shares) == 0 {
		return result, cfg, errors.New("配置尚未授权共享目录")
	}
	return result, cfg, nil
}

// PathPublisher owns at most one local publication. Jobs survive navigation,
// can be canceled, and are joined before the desktop exits.
type PathPublisher struct {
	mu     sync.Mutex
	job    PublicationJob
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *PathPublisher) Start(parent context.Context, config, revision, share, path string) (PublicationJob, error) {
	if publication.ValidatePath(path) != nil {
		return PublicationJob{}, errors.New("请输入共享内相对文件或文件夹路径")
	}
	inspected, cfg, err := readPublicationConfig(config)
	if err != nil {
		return PublicationJob{}, err
	}
	if inspected.Revision != revision {
		return PublicationJob{}, errors.New("Agent 配置已变化，请重新读取共享后发布")
	}
	root := ""
	for _, s := range cfg.Shares {
		if s.ID == share {
			root = s.Path
		}
	}
	if root == "" {
		return PublicationJob{}, errors.New("请选择此配置中的共享")
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return PublicationJob{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return PublicationJob{}, errors.New("已有本机发布正在运行，请完成或取消后再试")
	}
	ctx, cancel := context.WithCancel(parent)
	job := PublicationJob{ID: hex.EncodeToString(id), Path: path, State: "publishing"}
	done := make(chan struct{})
	p.done = done
	p.cancel = cancel
	p.job = job
	go func() {
		defer cancel()
		value, err := publication.Publish(ctx, root, cfg.ContentDirectory, share, path, manifest.DefaultChunkSize)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.job.State = "completed"
		if err != nil {
			p.job.State = "failed"
			p.job.Error = err.Error()
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				p.job.State = "cancelled"
				p.job.Error = ""
			}
		} else {
			p.job.Version = &value
		}
		p.cancel = nil
		close(done)
	}()
	return job, nil
}
func (p *PathPublisher) Status() PublicationJob { p.mu.Lock(); defer p.mu.Unlock(); return p.job }
func (p *PathPublisher) Cancel(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.job.ID != id {
		return errors.New("发布任务已变化")
	}
	if p.cancel != nil {
		p.job.State = "cancelling"
		p.cancel()
	}
	return nil
}
func (p *PathPublisher) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	done := p.done
	p.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
