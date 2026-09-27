package raft

// NodeID identifies a cluster member.
type NodeID string

// Term is a Raft term number (§5.1).
type Term uint64

// LogIndex is a 1-based log position; 0 means "no entry".
type LogIndex uint64

// LogEntry is a single replicated log entry. Command is opaque to Raft.
type LogEntry struct {
	Term    Term
	Index   LogIndex
	Command []byte
}
