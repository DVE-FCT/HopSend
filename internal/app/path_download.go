package app

import (
	"context"
	"errors"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
)

type PathVersionResult struct {
	Version *v1.PathVersion `json:"version,omitempty"`
	Error   *BrowseError    `json:"error,omitempty"`
}

// ResolvePathVersion only reads HTTPS control information; no SSH, deployment
// or publication is possible from this use case.
func ResolvePathVersion(parent context.Context, cfg nodeclient.Config, share, path string) PathVersionResult {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	c, err := nodeclient.New(cfg)
	if err != nil {
		return PathVersionResult{Error: browseFailure(err)}
	}
	defer c.Close()
	value, err := c.PathIndex(ctx, share, path)
	if err != nil {
		return PathVersionResult{Error: browseFailure(err)}
	}
	m, err := c.Manifest(ctx, share, value.ManifestID)
	if err == nil && (m.Manifest.ContentID != value.ContentID || m.Manifest.SizeBytes != value.SizeBytes || m.Manifest.Name != value.Name || (m.Manifest.PayloadKind == "folder") != (value.PayloadKind == "folder")) {
		err = errors.New("路径索引与已认证清单不一致，未选择此版本")
	}
	if err != nil {
		return PathVersionResult{Error: browseFailure(err)}
	}
	return PathVersionResult{Version: &value}
}
