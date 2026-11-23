package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type eventLog struct {
	mu   sync.Mutex
	path string
	last map[string]string
	err  error
}

func newEventLog(database string) *eventLog {
	return &eventLog{path: filepath.Join(filepath.Dir(database), "logs", "tasks.jsonl"), last: make(map[string]string)}
}

func (l *eventLog) append(s TaskStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := s.State + "\x00" + s.Error
	if l.last[s.TaskID] == key {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0700); err != nil {
		l.err = err
		return
	}
	if info, err := os.Stat(l.path); err == nil && info.Size() > 1<<20 {
		if err := os.Remove(l.path + ".1"); err != nil && !os.IsNotExist(err) {
			l.err = err
			return
		}
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			l.err = err
			return
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		l.err = err
		return
	}
	// Only task status is recorded. Source configurations and credential file
	// contents are never included; OS errors may contain local runtime paths.
	l.err = json.NewEncoder(f).Encode(s)
	if err := f.Close(); l.err == nil {
		l.err = err
	}
	if l.err == nil {
		l.last[s.TaskID] = key
	}
}

func (l *eventLog) read() ([]TaskStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return []TaskStatus{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := []TaskStatus{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var value TaskStatus
		if json.Unmarshal(scanner.Bytes(), &value) == nil {
			values = append(values, value)
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) > 200 {
		values = values[len(values)-200:]
	}
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values, nil
}
