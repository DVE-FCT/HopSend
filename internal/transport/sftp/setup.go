package sftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Command owns one authenticated SSH exec channel. Keep its Client open while
// a foreground remote Agent is needed; Windows OpenSSH owns child lifetimes.
type Command struct {
	session interface{ Close() error }
	done    chan struct{}
	output  cappedOutput
	err     error
}
type cappedOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if left := 16384 - w.data.Len(); left > 0 {
		_, _ = w.data.Write(p[:min(len(p), left)])
	}
	return n, nil
}
func (w *cappedOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.data.Bytes()...)
}
func (c *Client) StartCommand(ctx context.Context, command string) (*Command, error) {
	s, err := c.ssh.NewSession()
	if err != nil {
		return nil, err
	}
	result := &Command{session: s, done: make(chan struct{})}
	s.Stdout = &result.output
	s.Stderr = &result.output
	if err = s.Start(command); err != nil {
		_ = s.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	go func() {
		result.err = s.Wait()
		if ctx.Err() != nil {
			result.err = ctx.Err()
		}
		stop()
		_ = s.Close()
		close(result.done)
	}()
	return result, nil
}
func (c *Command) Wait(ctx context.Context) ([]byte, error) {
	select {
	case <-c.done:
		return c.output.Bytes(), c.err
	case <-ctx.Done():
		_ = c.session.Close()
		return nil, ctx.Err()
	}
}
func (c *Command) Exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}
func (c *Client) RunCommand(ctx context.Context, command string) ([]byte, error) {
	p, err := c.StartCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	return p.Wait(ctx)
}

// PutNew is reserved for explicitly authorized preparation artifacts. Normal
// transfer writes still use Agent-created upload sessions and WriteRange.
func (c *Client) PutNew(ctx context.Context, path string, input io.Reader, length int64) error {
	if length < 0 {
		return errors.New("准备文件长度无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.ssh.Close() })
	defer stop()
	f, err := c.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	n, copyErr := io.CopyBuffer(f, io.LimitReader(input, length+1), make([]byte, 1<<20))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n != length {
		_ = c.sftp.Remove(path)
		return errors.Join(copyErr, closeErr, fmt.Errorf("准备文件写入 %d/%d 字节", n, length))
	}
	return nil
}
func (c *Client) MakeDirectory(path string) error         { return c.sftp.MkdirAll(path) }
func (c *Client) RemovePreparationFile(path string) error { return c.sftp.Remove(path) }
