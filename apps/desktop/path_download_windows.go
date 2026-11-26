package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"time"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	"github.com/DVE-FCT/HopSend/internal/app"
)

type pathReview struct {
	device   Device
	version  v1.PathVersion
	expires  time.Time
	starting bool
}

type PathSelection struct {
	ReviewID string           `json:"review_id"`
	Version  *v1.PathVersion  `json:"version,omitempty"`
	Error    *app.BrowseError `json:"error,omitempty"`
}

func (a *App) InspectPublishedPath(id, share, path string) (PathSelection, error) {
	if err := a.Ready(); err != nil {
		return PathSelection{}, err
	}
	d, err := a.findDevice(id)
	if err != nil {
		return PathSelection{}, err
	}
	result := app.ResolvePathVersion(a.ctx, d.request().Agent, share, path)
	if result.Error != nil {
		return PathSelection{Error: result.Error}, nil
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return PathSelection{}, err
	}
	reviewID := hex.EncodeToString(key)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pathReviews == nil {
		a.pathReviews = make(map[string]pathReview)
	}
	for id, review := range a.pathReviews {
		if time.Now().After(review.expires) {
			delete(a.pathReviews, id)
		}
	}
	if len(a.pathReviews) >= 64 {
		return PathSelection{}, errors.New("待确认版本过多，请关闭后稍后重试")
	}
	a.pathReviews[reviewID] = pathReview{device: d, version: *result.Version, expires: time.Now().Add(10 * time.Minute)}
	return PathSelection{ReviewID: reviewID, Version: result.Version}, nil
}

// StartPublishedDownload consumes one reviewed version, retaining its original
// share and manifest even if the source has published another version meanwhile.
func (a *App) StartPublishedDownload(reviewID, destination string) (string, error) {
	if err := a.Ready(); err != nil {
		return "", err
	}
	if destination == "" {
		return "", errors.New("请选择本机保存位置")
	}
	a.mu.Lock()
	review, ok := a.pathReviews[reviewID]
	if !ok || review.starting || time.Now().After(review.expires) {
		a.mu.Unlock()
		return "", errors.New("版本选择已过期或正在创建任务，请重新选择")
	}
	current := false
	for _, d := range a.devices {
		if d == review.device {
			current = true
			break
		}
	}
	if !current {
		delete(a.pathReviews, reviewID)
		a.mu.Unlock()
		return "", errors.New("设备或信任资料已变化，请重新选择版本")
	}
	review.starting = true
	a.pathReviews[reviewID] = review
	a.mu.Unlock()
	success := false
	defer func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if success {
			delete(a.pathReviews, reviewID)
		} else if r, exists := a.pathReviews[reviewID]; exists {
			r.starting = false
			a.pathReviews[reviewID] = r
		}
	}()
	r := review.device.request()
	r.ShareID = review.version.ShareID
	r.ManifestID = review.version.ManifestID
	r.Destination = destination
	id, err := a.manager.Start(a.ctx, r)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.pending == nil {
		a.pending = make(map[string]TaskView)
	}
	a.pending[id] = TaskView{ID: id, Name: filepath.Base(destination), Destination: destination, ManifestID: r.ManifestID}
	a.mu.Unlock()
	success = true
	return id, nil
}

func (a *App) ReleasePublishedPath(reviewID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if review, ok := a.pathReviews[reviewID]; ok && !review.starting {
		delete(a.pathReviews, reviewID)
	}
}
