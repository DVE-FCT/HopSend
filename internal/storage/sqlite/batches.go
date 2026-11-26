package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	mv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type BatchRecipient struct {
	TaskID       string          `json:"task_id"`
	BatchID      string          `json:"batch_id"`
	DeviceID     string          `json:"device_id"`
	DeviceName   string          `json:"device_name"`
	Request      json.RawMessage `json:"-"`
	State        string          `json:"state"`
	Error        string          `json:"error,omitempty"`
	SnapshotHeld bool            `json:"snapshot_held"`
}
type UploadBatch struct {
	ID                 string           `json:"id"`
	Intent             string           `json:"-"`
	Name               string           `json:"name"`
	Manifest           mv1.Manifest     `json:"manifest"`
	SnapshotDirectory  string           `json:"-"`
	State              string           `json:"state"`
	CreatedAt          string           `json:"created_at"`
	Recipients         []BatchRecipient `json:"recipients"`
	SnapshotReferences int              `json:"snapshot_references"`
}

func (s *Store) CreateBatch(ctx context.Context, b UploadBatch) error {
	if !manifest.ValidID(b.Intent) || manifest.Validate(b.Manifest) != nil || len(b.Recipients) < 1 || len(b.Recipients) > 32 {
		return errors.New("批次内容或接收数量无效")
	}
	raw, err := json.Marshal(b.Manifest)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO upload_batches(id,intent,name,manifest,content_id,snapshot_directory,state,created_at,updated_at) VALUES(?,?,?,?,?,?,'running',?,?)`, b.ID, b.Intent, b.Name, string(raw), b.Manifest.ContentID, b.SnapshotDirectory, now(), now()); err != nil {
		return err
	}
	for _, item := range b.Recipients {
		if !manifest.ValidID(item.TaskID) || !json.Valid(item.Request) {
			return errors.New("批次任务引用无效")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO batch_recipients(task_id,batch_id,device_id,device_name,request,state,error,snapshot_held,updated_at) VALUES(?,?,?,?,?,'queued','',1,?)`, item.TaskID, b.ID, item.DeviceID, item.DeviceName, string(item.Request), now()); err != nil {
			return errors.New("目标任务已属于其他批次，未重复创建")
		}
	}
	return tx.Commit()
}

func (s *Store) Batch(ctx context.Context, id string) (UploadBatch, error) {
	var b UploadBatch
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT id,intent,name,manifest,snapshot_directory,state,created_at FROM upload_batches WHERE id=?`, id).Scan(&b.ID, &b.Intent, &b.Name, &raw, &b.SnapshotDirectory, &b.State, &b.CreatedAt); err != nil {
		return b, err
	}
	if err := json.Unmarshal([]byte(raw), &b.Manifest); err != nil {
		return b, err
	}
	if err := manifest.Validate(b.Manifest); err != nil {
		return b, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT task_id,batch_id,device_id,device_name,request,state,error,snapshot_held FROM batch_recipients WHERE batch_id=? ORDER BY rowid`, id)
	if err != nil {
		return b, err
	}
	b.Recipients = []BatchRecipient{}
	for rows.Next() {
		var item BatchRecipient
		var request string
		if err := rows.Scan(&item.TaskID, &item.BatchID, &item.DeviceID, &item.DeviceName, &request, &item.State, &item.Error, &item.SnapshotHeld); err != nil {
			rows.Close()
			return b, err
		}
		item.Request = json.RawMessage(request)
		b.Recipients = append(b.Recipients, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, err
	}
	// Different manifests (e.g. filenames) may share the same content object.
	// Count every live reference to that object in this snapshot store.
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM batch_recipients r JOIN upload_batches b ON b.id=r.batch_id WHERE b.snapshot_directory=? AND b.content_id=? AND r.snapshot_held=1`, b.SnapshotDirectory, b.Manifest.ContentID).Scan(&b.SnapshotReferences)
	return b, err
}

func (s *Store) Batches(ctx context.Context) ([]UploadBatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM upload_batches ORDER BY created_at DESC LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 1000 {
		return nil, errors.New("批次历史超过1000项，请先归档，未返回截断结果")
	}
	result := make([]UploadBatch, 0, len(ids))
	for _, id := range ids {
		b, err := s.Batch(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}

func (s *Store) SetBatchMode(ctx context.Context, id, state string) error {
	if state != "running" && state != "paused" && state != "cancelled" {
		return errors.New("批次模式无效")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE upload_batches SET state=?,updated_at=? WHERE id=?`, state, now(), id)
	return err
}

func (s *Store) QueuedBatchRecipients(ctx context.Context) ([]BatchRecipient, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.task_id,r.batch_id,r.device_id,r.device_name,r.request FROM batch_recipients r JOIN upload_batches b ON b.id=r.batch_id WHERE r.state='queued' AND b.state='running' ORDER BY r.updated_at LIMIT 128`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BatchRecipient{}
	for rows.Next() {
		var r BatchRecipient
		var raw string
		if err := rows.Scan(&r.TaskID, &r.BatchID, &r.DeviceID, &r.DeviceName, &raw); err != nil {
			return nil, err
		}
		r.Request = json.RawMessage(raw)
		r.State = "queued"
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) BatchForTask(ctx context.Context, id string) (string, error) {
	var batch string
	err := s.db.QueryRowContext(ctx, `SELECT batch_id FROM batch_recipients WHERE task_id=?`, id).Scan(&batch)
	return batch, err
}
func (s *Store) SetBatchRecipient(ctx context.Context, id, state, message string) error {
	valid := map[string]bool{"queued": true, "running": true, "pausing": true, "cancelling": true, "paused": true, "failed": true, "completed": true, "cancelled": true}
	if !valid[state] {
		return errors.New("批次任务状态无效")
	}
	held := state != "completed" && state != "cancelled"
	_, err := s.db.ExecContext(ctx, `UPDATE batch_recipients SET state=?,error=?,snapshot_held=?,updated_at=? WHERE task_id=?`, state, message, held, now(), id)
	return err
}

func (s *Store) RecoverBatchQueue(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`UPDATE batch_recipients SET state=(SELECT state FROM tasks WHERE tasks.id=batch_recipients.task_id),snapshot_held=0 WHERE task_id IN(SELECT id FROM tasks WHERE state IN('completed','cancelled'))`,
		`UPDATE batch_recipients SET state='paused',error='' WHERE state IN('queued','running','pausing','cancelling')`,
		`UPDATE upload_batches SET state='paused' WHERE state='running'`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return tx.Commit()
}
