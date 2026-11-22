package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	v1 "github.com/DVE-FCT/HopSend/contracts/manifest/v1"
)

// Task contains recovery data and credential references, never credential bytes.
type Task struct {
	ID          string          `json:"id"`
	Manifest    v1.Manifest     `json:"manifest"`
	RemotePath  string          `json:"remote_path"`
	Destination string          `json:"destination"`
	Staging     string          `json:"staging"`
	State       string          `json:"state"`
	Confirmed   []int           `json:"confirmed_chunks"`
	Source      json.RawMessage `json:"source"`
	UpdatedAt   string          `json:"updated_at"`
}

type Journal interface {
	Prepare(context.Context, Task) (Task, error)
	SetState(context.Context, string, string) error
	SetChunk(context.Context, string, int, bool) error
}

func TaskID(manifestID, destination string) string {
	destination = filepath.Clean(destination)
	if runtime.GOOS == "windows" {
		destination = strings.ToLower(destination)
	}
	sum := sha256.Sum256([]byte(manifestID + "\x00" + destination))
	return hex.EncodeToString(sum[:])
}
