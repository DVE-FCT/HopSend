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
	if version != 1 || synchronous != 2 || mode != "wal" || foreign != 1 {
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
