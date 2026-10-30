// Package storage implements safe in-memory storage for Raft nodes.
package storage

import (
	"sync"

	"raftgo/raft"
)

// Memory stores one node's term and vote in RAM. Its zero value is ready for
// use. A simulated node restart must reuse the same Memory; a new instance
// represents a fresh node, not recovery after process termination.
// Memory must not be copied after first use.
type Memory struct {
	mu    sync.Mutex
	state raft.PersistentState
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
