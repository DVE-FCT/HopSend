package nodeclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

func validSession(id string) bool { b, err := hex.DecodeString(id); return err == nil && len(b) == 16 }

func (c *Client) CreateUpload(ctx context.Context, request v1.CreateUpload) (v1.UploadSession, error) {
	var s v1.UploadSession
	err := c.request(ctx, http.MethodPost, "/v1/uploads", request, &s, false)
	if err == nil && (!validSession(s.ID) || s.RequestID != request.RequestID || s.ManifestID != request.Manifest.ManifestID || s.ShareID != request.ShareID || s.Target != request.Target || s.SFTPPath == "") {
		err = errors.New("接收端上传会话与请求不一致")
	}
	return s, err
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
	if err == nil && (s.ID != id || !manifest.ValidID(s.ManifestID) || s.SFTPPath == "") {
		err = errors.New("接收端会话响应身份无效")
	}
	return s, err
}
