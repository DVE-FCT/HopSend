package main

import (
	"encoding/json"
	"errors"

	"github.com/DVE-FCT/HopSend/internal/app"
	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/storage/sqlite"
)

type BatchUploadTarget struct {
	DeviceID   string `json:"device_id"`
	ShareID    string `json:"share_id"`
	ParentPath string `json:"parent_path"`
	Target     string `json:"target"`
}
type BatchRecipientView struct {
	ID           string `json:"id"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	ShareID      string `json:"share_id"`
	ParentPath   string `json:"parent_path"`
	Target       string `json:"target"`
	Destination  string `json:"destination"`
	SnapshotHeld bool   `json:"snapshot_held"`
	app.TaskStatus
}
type BatchView struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	ManifestID         string               `json:"manifest_id"`
	ContentID          string               `json:"content_id"`
	SizeBytes          int64                `json:"size_bytes"`
	State              string               `json:"state"`
	CreatedAt          string               `json:"created_at"`
	SnapshotReferences int                  `json:"snapshot_references"`
	Recipients         []BatchRecipientView `json:"recipients"`
}

func (a *App) batchView(b sqlite.UploadBatch) (BatchView, error) {
	view := BatchView{ID: b.ID, Name: b.Name, ManifestID: b.Manifest.ManifestID, ContentID: b.Manifest.ContentID, SizeBytes: b.Manifest.SizeBytes, State: b.State, CreatedAt: b.CreatedAt, SnapshotReferences: b.SnapshotReferences, Recipients: []BatchRecipientView{}}
	complete, cancelled, failed := 0, 0, 0
	for _, item := range b.Recipients {
		var r app.DownloadRequest
		if err := json.Unmarshal(item.Request, &r); err != nil {
			return view, err
		}
		status := app.TaskStatus{Progress: engine.Progress{TaskID: item.TaskID, State: item.State, TotalBytes: b.Manifest.SizeBytes}, Error: item.Error}
		if task, err := a.store.Get(a.ctx, item.TaskID); err == nil {
			status.CompletedChunks = len(task.Confirmed)
			for _, i := range task.Confirmed {
				status.VerifiedBytes += task.Manifest.Chunks[i].Length
			}
		}
		if live, ok := a.manager.Status(item.TaskID); ok && item.State == "running" {
			status = live
		}
		if item.State == "completed" {
			complete++
			status.VerifiedBytes = b.Manifest.SizeBytes
			status.CompletedChunks = len(b.Manifest.Chunks)
		}
		if item.State == "cancelled" {
			cancelled++
		}
		if item.State == "failed" {
			failed++
		}
		view.Recipients = append(view.Recipients, BatchRecipientView{ID: item.TaskID, DeviceID: item.DeviceID, DeviceName: item.DeviceName, ShareID: r.ShareID, ParentPath: r.UploadParentPath, Target: r.UploadTarget, Destination: r.Destination, SnapshotHeld: item.SnapshotHeld, TaskStatus: status})
	}
	if complete == len(b.Recipients) {
		view.State = "completed"
	} else if complete+cancelled == len(b.Recipients) {
		view.State = "cancelled"
	} else if failed > 0 && complete+cancelled+failed == len(b.Recipients) {
		view.State = "attention"
	}
	return view, nil
}
func (a *App) StartBatchUpload(key, source string, targets []BatchUploadTarget) (BatchView, error) {
	if err := a.Ready(); err != nil {
		return BatchView{}, err
	}
	if a.batches == nil {
		return BatchView{}, errors.New("批次队列尚未就绪")
	}
	items := []app.BatchTarget{}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.DeviceID] {
			return BatchView{}, errors.New("同一设备在一个批次中只能选择一次")
		}
		seen[target.DeviceID] = true
		d, err := a.findDevice(target.DeviceID)
		if err != nil {
			return BatchView{}, err
		}
		r := d.request()
		r.ShareID = target.ShareID
		r.UploadParentPath = target.ParentPath
		r.UploadTarget = target.Target
		items = append(items, app.BatchTarget{DeviceID: d.ID, DeviceName: d.Name, Request: r})
	}
	b, err := a.batches.Create(a.ctx, key, source, items)
	if err != nil {
		return BatchView{}, err
	}
	return a.batchView(b)
}
func (a *App) UploadBatches() ([]BatchView, error) {
	if err := a.Ready(); err != nil {
		return nil, err
	}
	if a.batches == nil {
		return []BatchView{}, nil
	}
	batches, err := a.batches.List(a.ctx)
	if err != nil {
		return nil, err
	}
	result := make([]BatchView, 0, len(batches))
	for _, b := range batches {
		v, err := a.batchView(b)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}
func (a *App) ControlUploadBatch(id, action string) error {
	if err := a.Ready(); err != nil {
		return err
	}
	if a.batches == nil {
		return errors.New("批次队列未就绪")
	}
	return a.batches.Control(a.ctx, id, "", action)
}
