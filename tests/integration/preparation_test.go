package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"testing"
	"time"

	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
	"golang.org/x/crypto/ssh"
)

func TestPreparationArtifactsAndExecUseVerifiedSSHAndRespectCancellation(t *testing.T) {
	cfg, _, _, _ := startSFTPControlled(t, 0, func(command string, ch ssh.Channel) {
		if command == "wait" {
			_, _ = io.Copy(io.Discard, ch)
			return
		}
		_, _ = ch.Write([]byte("ready"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := transport.Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	path := filepath.ToSlash(filepath.Join(filepath.Dir(cfg.PrivateKeyPath), "preparation.bin"))
	data := bytes.Repeat([]byte("probe"), 6554)
	if err = c.PutNew(ctx, path, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if err = c.PutNew(ctx, path, bytes.NewReader([]byte("changed")), 7); err == nil {
		t.Fatal("preparation overwrote an existing artifact")
	}
	hash := sha256.New()
	if _, err = c.ReadRange(ctx, path, 0, int64(len(data)), hash); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(data)
	if hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(expected[:]) {
		t.Fatal("preparation bytes differ")
	}
	output, err := c.RunCommand(ctx, "ready")
	if err != nil || string(output) != "ready" {
		t.Fatal(string(output), err)
	}
	commandCtx, stop := context.WithCancel(ctx)
	command, err := c.StartCommand(commandCtx, "wait")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	wait, finish := context.WithTimeout(ctx, 2*time.Second)
	defer finish()
	if _, err = command.Wait(wait); err == nil {
		t.Fatal("cancelled command reported success")
	}
	if err = c.RemovePreparationFile(path); err != nil {
		t.Fatal(err)
	}
}
