package nodeclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

func validSession(id string) bool { b, err := hex.DecodeString(id); return err == nil && len(b) == 16 }

func (c *Client) CreateUpload(ctx context.Context, request v1.CreateUpload) (v1.UploadSession, error) {
	var s v1.UploadSession
	parent, err := filesystem.UploadParent(request.ParentPath)
	if err != nil {
		return s, err
	}
	request.ParentPath = parent
	if parent != "" {
		if err := c.RequireUploadParent(ctx); err != nil {
			return s, err
		}
	}
	err = c.request(ctx, http.MethodPost, "/v1/uploads", request, &s, false)
	if err == nil && (!validSession(s.ID) || s.RequestID != request.RequestID || s.ManifestID != request.Manifest.ManifestID || s.ShareID != request.ShareID || s.Target != request.Target || s.ParentPath != request.ParentPath || s.SFTPPath == "") {
		err = errors.New("接收端上传会话与请求不一致")
	}
	return s, err
}

func (c *Client) RequireUploadParent(ctx context.Context) error {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.APIVersion != "v1" || !slices.Contains(caps.Operations, "upload_parent_path") {
		return errors.New("接收端不支持上传子目录，请升级 Agent 或明确选择共享根")
	}
	return nil
}

func (c *Client) UploadStatus(ctx context.Context, id string) (v1.UploadSession, error) {
	return c.uploadOperation(ctx, id, "", true)
}
func (c *Client) ConfirmUpload(ctx context.Context, id string, index int) (v1.UploadSession, error) {
	return c.uploadOperation(ctx, id, fmt.Sprintf("/chunks/%d/confirm", index), false)
}
func (c *Client) FinalizeUpload(ctx context.Context, id string) (v1.UploadSession, error) {
	return c.uploadOperation(ctx, id, "/finalize", true)
}

func (c *Client) uploadOperation(ctx context.Context, id, suffix string, long bool) (v1.UploadSession, error) {
	var s v1.UploadSession
	if !validSession(id) {
		return s, errors.New("上传会话 ID 无效")
	}
	method := http.MethodGet
	if suffix != "" {
		method = http.MethodPost
	}
	err := c.request(ctx, method, "/v1/uploads/"+id+suffix, nil, &s, long)
	parent, parentErr := filesystem.UploadParent(s.ParentPath)
	if err == nil && (parentErr != nil || parent != s.ParentPath || s.ID != id || !manifest.ValidID(s.ManifestID) || s.SFTPPath == "") {
		err = errors.New("接收端会话响应身份无效")
	}
	return s, err
}
