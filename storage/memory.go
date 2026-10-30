// Package storage implements safe in-memory storage for Raft nodes.
package storage

import (
	"fmt"
	"sync"

	"raftgo/raft"
)

// Memory stores one node's state and log in RAM. Its zero value is ready for
// use. A simulated node restart must reuse the same Memory; a new instance
// represents a fresh node, not recovery after process termination.
// Memory must not be copied after first use.
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
	// Allocate the replacement before publishing it, including both levels of
	// input slices. A rejected index leaves the entire stored log unchanged.
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
