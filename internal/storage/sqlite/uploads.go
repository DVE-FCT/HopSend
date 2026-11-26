package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/DVE-FCT/HopSend/contracts/control/v1"
	manifestv1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
	"github.com/DVE-FCT/HopSend/internal/filesystem"
	"github.com/DVE-FCT/HopSend/internal/manifest"
)

type UploadRecord struct {
	v1.UploadSession
	OwnerID        string
	Manifest       manifestv1.Manifest
	Staging        string
	ParentIdentity string
}

func (s *Store) CreateUpload(ctx context.Context, owner string, request v1.CreateUpload, id, staging, sftpPath, parentIdentity string) (UploadRecord, error) {
	var record UploadRecord
	b, err := json.Marshal(request.Manifest)
	if err != nil {
		return record, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO uploads(id,owner_id,request_id,share_id,target,manifest,staging,sftp_path,state,updated_at,parent_path,parent_identity,manifest_scope) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_id,request_id) DO NOTHING`, id, owner, request.RequestID, request.ShareID, request.Target, string(b), staging, sftpPath, "uploading", now(), request.ParentPath, parentIdentity, request.ManifestScope)
	if err != nil {
		return record, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT id FROM uploads WHERE owner_id=? AND request_id=?`, owner, request.RequestID).Scan(&id)
	if err != nil {
		return record, err
	}
	record, err = s.Upload(ctx, id)
	if err == nil && (record.Manifest.ManifestID != request.Manifest.ManifestID || record.Target != request.Target || record.ShareID != request.ShareID || record.ParentPath != request.ParentPath || record.ParentIdentity != parentIdentity || record.ManifestScope != request.ManifestScope) {
		return record, errors.New("上传请求 ID 已绑定其他内容或目标")
	}
	return record, err
}

func (s *Store) FindUpload(ctx context.Context, owner, requestID string) (UploadRecord, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM uploads WHERE owner_id=? AND request_id=?`, owner, requestID).Scan(&id); err != nil {
		return UploadRecord{}, err
	}
	return s.Upload(ctx, id)
}

func (s *Store) Upload(ctx context.Context, id string) (UploadRecord, error) {
	var r UploadRecord
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT id,owner_id,request_id,share_id,target,manifest,staging,sftp_path,state,parent_path,parent_identity,manifest_scope FROM uploads WHERE id=?`, id).Scan(&r.ID, &r.OwnerID, &r.RequestID, &r.ShareID, &r.Target, &raw, &r.Staging, &r.SFTPPath, &r.State, &r.ParentPath, &r.ParentIdentity, &r.ManifestScope)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal([]byte(raw), &r.Manifest); err != nil {
		return r, err
	}
	if err = manifest.Validate(r.Manifest); err != nil {
		return r, err
	}
	parent, err := filesystem.UploadParent(r.ParentPath)
	if err != nil || parent != r.ParentPath || filesystem.SafeLeaf(r.Target) != nil || (r.ManifestScope != "" && r.ManifestScope != "source_snapshot") || (r.ManifestScope == "" && r.ShareID != r.Manifest.ShareID) {
		return r, errors.New("上传目录或名称记录损坏")
	}
	r.ManifestID = r.Manifest.ManifestID
	r.Confirmed = []int{}
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_index FROM upload_chunks WHERE upload_id=? ORDER BY chunk_index`, id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		if err = rows.Scan(&i); err != nil {
			return r, err
		}
		if i < 0 || i >= len(r.Manifest.Chunks) {
			return r, errors.New("上传确认块引用越界")
		}
		r.Confirmed = append(r.Confirmed, i)
	}
	if r.State == "completed" {
		r.SHA256 = r.Manifest.ContentID
	}
	return r, rows.Err()
}

func (s *Store) SetUploadState(ctx context.Context, id, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE uploads SET state=?,updated_at=? WHERE id=?`, state, now(), id)
	return err
}

func (s *Store) SetUploadChunk(ctx context.Context, id string, index int, confirmed bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if confirmed {
		_, err = tx.ExecContext(ctx, `INSERT INTO upload_chunks VALUES(?,?) ON CONFLICT DO NOTHING`, id, index)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM upload_chunks WHERE upload_id=? AND chunk_index=?`, id, index)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE uploads SET updated_at=? WHERE id=?`, now(), id); err != nil {
		return err
	}
	return tx.Commit()
}
