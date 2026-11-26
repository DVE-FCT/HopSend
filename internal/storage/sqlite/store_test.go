package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func TestMigrationDurabilityAndUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version, synchronous, foreign int
	var mode string
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("PRAGMA foreign_keys").Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if version != 3 || synchronous != 2 || mode != "wal" || foreign != 1 {
		t.Fatal(version, synchronous, mode, foreign)
	}
	if err = s.SetState(context.Background(), "absent", "completed"); err == nil {
		t.Fatal("missing task accepted")
	}
	if err = s.SetChunk(context.Background(), "absent", 0, true); err == nil {
		t.Fatal("foreign key disabled")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal("reopen migration:", err)
	}
	_ = s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if s, err = Open(path); err == nil {
		_ = s.Close()
		t.Fatal("future database version modified")
	}
}

func TestConcurrentVersionTwoOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE uploads(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,request_id TEXT NOT NULL,share_id TEXT NOT NULL,target TEXT NOT NULL,manifest TEXT NOT NULL,staging TEXT NOT NULL,sftp_path TEXT NOT NULL,state TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(owner_id,request_id))`,
		`PRAGMA user_version=2`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Close()
	start := make(chan struct{})
	results := make(chan error, 6)
	var group sync.WaitGroup
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			s, err := Open(path)
			if err == nil {
				err = s.Close()
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("concurrent migration failed", err)
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal(version, err)
	}
}

func TestVersionOneMigrationKeepsExistingDownloadRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE tasks(id TEXT PRIMARY KEY,manifest TEXT NOT NULL,remote_path TEXT NOT NULL,destination TEXT NOT NULL,staging TEXT NOT NULL,state TEXT NOT NULL,source TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE chunks(task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,chunk_index INTEGER NOT NULL,PRIMARY KEY(task_id,chunk_index))`,
		`INSERT INTO tasks VALUES('existing-task','{}','/snapshot','target','staging','paused','{}','original-time')`,
		`INSERT INTO chunks VALUES('existing-task',7)`,
		`PRAGMA user_version=1`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state, updated string
	var index, version int
	if err = s.db.QueryRow(`SELECT state,updated_at FROM tasks WHERE id='existing-task'`).Scan(&state, &updated); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT chunk_index FROM chunks WHERE task_id='existing-task'`).Scan(&index); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if state != "paused" || updated != "original-time" || index != 7 || version != 3 {
		t.Fatal("migration changed existing recovery data", state, updated, index, version)
	}
}

func TestVersionTwoUploadMigrationPreservesRowsWithoutInventingParentTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE uploads(id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,request_id TEXT NOT NULL,share_id TEXT NOT NULL,target TEXT NOT NULL,manifest TEXT NOT NULL,staging TEXT NOT NULL,sftp_path TEXT NOT NULL,state TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(owner_id,request_id))`,
		`INSERT INTO uploads VALUES('old','owner','request','files','target.bin','{}','staging','/staging','uploading','old-time')`,
		`PRAGMA user_version=2`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var parent, identity, state, stamp string
	if err := s.db.QueryRow(`SELECT parent_path,parent_identity,state,updated_at FROM uploads WHERE id='old'`).Scan(&parent, &identity, &state, &stamp); err != nil {
		t.Fatal(err)
	}
	if parent != "" || identity != "" || state != "uploading" || stamp != "old-time" {
		t.Fatal("legacy row changed or implicit trust created", parent, identity, state, stamp)
	}
	backups, err := filepath.Glob(path + ".pre-v3-*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatal("missing consistent pre-upgrade backup", backups, err)
	}
	backup, err := sql.Open("sqlite", backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var oldVersion int
	if err := backup.QueryRow("PRAGMA user_version").Scan(&oldVersion); err != nil || oldVersion != 2 {
		t.Fatal("backup does not retain original schema", oldVersion, err)
	}
	if err := backup.QueryRow("SELECT state FROM uploads WHERE id='old'").Scan(&state); err != nil || state != "uploading" {
		t.Fatal("committed WAL data missing in backup", state, err)
	}
}
