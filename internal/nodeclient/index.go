package nodeclient

import (
	"context"
	"errors"
	"net/url"
	"slices"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/publication"
)

// PathIndex resolves an explicit published version. It never scans or publishes
// the live source. Older Agents are rejected without guessing from filenames.
func (c *Client) PathIndex(ctx context.Context, share, path string) (v1.PathVersion, error) {
	var value v1.PathVersion
	if publication.ValidatePath(path) != nil {
		return value, errors.New("路径必须为共享内的规范相对路径")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return value, err
	}
	if caps.APIVersion != "v1" || !slices.Contains(caps.Operations, "published_path_index") {
		return value, errors.New("此 Agent 不支持已发布路径索引，请升级或使用清单 ID")
	}
	err = c.get(ctx, "/v1/shares/"+url.PathEscape(share)+"/index?"+url.Values{"path": {path}}.Encode(), &value)
	if err == nil {
		err = publication.Validate(value, share, path)
	}
	return value, err
}
