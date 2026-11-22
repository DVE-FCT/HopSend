package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
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
	if version != 2 || synchronous != 2 || mode != "wal" || foreign != 1 {
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
	if state != "paused" || updated != "original-time" || index != 7 || version != 2 {
		t.Fatal("migration changed existing recovery data", state, updated, index, version)
	}
}
