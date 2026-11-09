// Package storage хранит состояние узлов в RAM.
package storage

import (
	"fmt"
	"sync"

	"raftgo/raft"
)

// Memory хранит term, голос и лог в RAM; при перезапуске тестового узла нужен тот же объект.
type Memory struct {
	mu    sync.Mutex
	state raft.PersistentState
	log   []raft.LogEntry
}

var _ raft.Storage = (*Memory)(nil)

func (m *Memory) LoadState() (raft.PersistentState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, nil
}

func (m *Memory) SaveState(state raft.PersistentState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	return nil
}

func (m *Memory) LoadLog() ([]raft.LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneLog(m.log), nil
}

func (m *Memory) StoreLog(from raft.Index, entries []raft.LogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if from == 0 || from > raft.Index(len(m.log))+1 {
		return fmt.Errorf("invalid log index %d", from)
	}
	// Копируем префикс и новые записи, чтобы хранилище не зависело от чужих срезов.
	m.log = append(cloneLog(m.log[:from-1]), cloneLog(entries)...)
	return nil
}

func cloneLog(entries []raft.LogEntry) []raft.LogEntry {
	out := make([]raft.LogEntry, len(entries))
	for i, e := range entries {
		out[i] = e
		out[i].Command = append([]byte(nil), e.Command...)
	}
	return out
}
