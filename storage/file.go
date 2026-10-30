// Package storage provides Raft storage backends.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"raftgo/raft"
)

type snapshot struct {
	State raft.PersistentState
	Log   []raft.LogEntry
}

type File struct {
	mu     sync.Mutex
	dir    string
	lock   *os.File
	data   snapshot
	failed error
}

func Open(dir string) (*File, error) {
	if err := makeDurableDir(dir); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("storage already in use: %w", err)
	}
	f := &File{dir: dir, lock: lock}
	b, err := os.ReadFile(filepath.Join(dir, "raft.json"))
	if err == nil {
		var fields map[string]json.RawMessage
		err = json.Unmarshal(b, &fields)
		if err == nil && (len(fields["State"]) == 0 || string(fields["State"]) == "null" || len(fields["Log"]) == 0) {
			err = fmt.Errorf("storage snapshot is missing state or log")
		}
		if err == nil {
			err = json.Unmarshal(b, &f.data)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (f *File) Close() error { return f.lock.Close() }

func (f *File) LoadState() (raft.PersistentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data.State, f.failed
}
func (f *File) LoadLog() ([]raft.LogEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneLog(f.data.Log), f.failed
}
func (f *File) SaveState(s raft.PersistentState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.save(snapshot{State: s, Log: f.data.Log})
}
func (f *File) StoreLog(from raft.Index, entries []raft.LogEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from == 0 || from > raft.Index(len(f.data.Log))+1 {
		return fmt.Errorf("invalid log index %d", from)
	}
	log := append(cloneLog(f.data.Log[:from-1]), cloneLog(entries)...)
	return f.save(snapshot{State: f.data.State, Log: log})
}

// Poison on any write failure: a later successful fsync cannot establish what
// survived an earlier I/O failure. Never acknowledge more writes until restart.
func (f *File) save(next snapshot) (err error) {
	if f.failed != nil {
		return f.failed
	}
	defer func() {
		if err != nil {
			f.failed = err
		}
	}()
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.dir, ".raft-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(f.dir, "raft.json")); err != nil {
		return err
	}
	dir, err := os.Open(f.dir)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	f.data = next
	return nil
}

// Persist directory creation too: syncing a file inside a new directory does
// not make that directory's entry in its parent durable.
func makeDurableDir(path string) error {
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := makeDurableDir(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	f, err := os.Open(parent)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
