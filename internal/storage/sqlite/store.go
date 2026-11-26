// Package sqlite implements the engine journal. Each confirmation transaction
// follows the engine's file Sync; WAL and synchronous=FULL protect DB commits.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DVE-FCT/HopSend/internal/engine"
	"github.com/DVE-FCT/HopSend/internal/manifest"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Store, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: p}
	if err = s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize() error {
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL"} {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 4 {
		return errors.New("任务数据库版本高于当前程序支持的版本")
	}
	if version == 4 {
		return nil
	}
	// VACUUM INTO obtains a consistent SQLite backup, including committed WAL
	// data. A raw copy of a live .db file would not provide that guarantee.
	if version > 0 {
		empty, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".pre-v4-*.bak")
		if err != nil {
			return err
		}
		backup := empty.Name()
		if err := empty.Close(); err != nil {
			return err
		}
		if _, err := s.db.Exec("VACUUM INTO ?", backup); err != nil {
			return errors.New("数据库升级前备份失败，原状态未迁移")
		}
		f, err := os.OpenFile(backup, os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err = conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 4 {
		_, err = conn.ExecContext(context.Background(), "COMMIT")
		return err
	}
	if version > 4 {
		return errors.New("并发升级后的数据库版本不受支持")
	}
	if version < 3 {
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY, manifest TEXT NOT NULL, remote_path TEXT NOT NULL, destination TEXT NOT NULL, staging TEXT NOT NULL, state TEXT NOT NULL, source TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS chunks(task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, chunk_index INTEGER NOT NULL, PRIMARY KEY(task_id,chunk_index))`,
			`CREATE TABLE IF NOT EXISTS uploads(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,request_id TEXT NOT NULL,share_id TEXT NOT NULL,target TEXT NOT NULL,manifest TEXT NOT NULL,staging TEXT NOT NULL,sftp_path TEXT NOT NULL,state TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(owner_id,request_id))`,
			`CREATE TABLE IF NOT EXISTS upload_chunks(upload_id TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,chunk_index INTEGER NOT NULL,PRIMARY KEY(upload_id,chunk_index))`,
		} {
			if _, err = conn.ExecContext(context.Background(), statement); err != nil {
				return err
			}
		}
		for _, statement := range []string{
			`ALTER TABLE uploads ADD COLUMN parent_path TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE uploads ADD COLUMN parent_identity TEXT NOT NULL DEFAULT ''`,
		} {
			if _, err = conn.ExecContext(context.Background(), statement); err != nil {
				return err
			}
		}
	}
	for _, statement := range []string{
		`ALTER TABLE uploads ADD COLUMN manifest_scope TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE upload_batches(id TEXT PRIMARY KEY,intent TEXT NOT NULL,name TEXT NOT NULL,manifest TEXT NOT NULL,content_id TEXT NOT NULL,snapshot_directory TEXT NOT NULL,state TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE INDEX batch_content ON upload_batches(snapshot_directory,content_id)`,
		`CREATE TABLE batch_recipients(task_id TEXT PRIMARY KEY,batch_id TEXT NOT NULL REFERENCES upload_batches(id),device_id TEXT NOT NULL,device_name TEXT NOT NULL,request TEXT NOT NULL,state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',snapshot_held INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL)`,
		`CREATE INDEX batch_queue ON batch_recipients(state,updated_at)`,
		`PRAGMA user_version=4`,
	} {
		if _, err = conn.ExecContext(context.Background(), statement); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(context.Background(), "COMMIT")
	return err
}

func (s *Store) Close() error { return s.db.Close() }
func now() string             { return time.Now().UTC().Format(time.RFC3339Nano) }

func (s *Store) Prepare(ctx context.Context, task engine.Task) (engine.Task, error) {
	if err := manifest.Validate(task.Manifest); err != nil {
		return task, err
	}
	if task.ID != engine.TaskID(task.Manifest.ManifestID, task.Destination) {
		return task, errors.New("任务 ID 与目标不一致")
	}
	b, err := json.Marshal(task.Manifest)
	if err != nil {
		return task, err
	}
	if !json.Valid(task.Source) {
		return task, errors.New("任务源配置无效")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, task.ID, string(b), task.RemotePath, task.Destination, task.Staging, "pending", string(task.Source), now())
	if err != nil {
		return task, err
	}
	existing, err := s.Get(ctx, task.ID)
	if err != nil {
		return task, err
	}
	if existing.Manifest.ManifestID != task.Manifest.ManifestID || existing.RemotePath != task.RemotePath || existing.Destination != task.Destination {
		return task, errors.New("已存任务的内容版本或路径不同")
	}
	// Allow a user to rotate credential references without changing content.
	_, err = s.db.ExecContext(ctx, `UPDATE tasks SET source=? WHERE id=?`, string(task.Source), task.ID)
	return existing, err
}

func (s *Store) Get(ctx context.Context, id string) (engine.Task, error) {
	var t engine.Task
	var m, source string
	err := s.db.QueryRowContext(ctx, `SELECT id,manifest,remote_path,destination,staging,state,source,updated_at FROM tasks WHERE id=?`, id).Scan(&t.ID, &m, &t.RemotePath, &t.Destination, &t.Staging, &t.State, &source, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(m), &t.Manifest); err != nil {
		return t, err
	}
	if err = manifest.Validate(t.Manifest); err != nil {
		return t, fmt.Errorf("任务清单损坏: %w", err)
	}
	if t.ID != engine.TaskID(t.Manifest.ManifestID, t.Destination) {
		return t, errors.New("数据库任务身份不一致")
	}
	t.Source = json.RawMessage(source)
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_index FROM chunks WHERE task_id=? ORDER BY chunk_index`, id)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		if err = rows.Scan(&index); err != nil {
			return t, err
		}
		if index < 0 || index >= len(t.Manifest.Chunks) {
			return t, errors.New("数据库分块引用越界")
		}
		t.Confirmed = append(t.Confirmed, index)
	}
	return t, rows.Err()
}

func (s *Store) List(ctx context.Context) ([]engine.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]engine.Task, 0, len(ids))
	for _, id := range ids {
		t, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *Store) SetState(ctx context.Context, id, state string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET state=?,updated_at=? WHERE id=?`, state, now(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("任务不存在")
	}
	return err
}

func (s *Store) SetChunk(ctx context.Context, id string, index int, confirmed bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if confirmed {
		_, err = tx.ExecContext(ctx, `INSERT INTO chunks VALUES(?,?) ON CONFLICT DO NOTHING`, id, index)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM chunks WHERE task_id=? AND chunk_index=?`, id, index)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now(), id); err != nil {
		return err
	}
	return tx.Commit()
}
