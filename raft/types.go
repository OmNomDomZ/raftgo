package raft

// NodeID identifies a cluster member.
type NodeID string

// Term is a Raft term number (§5.1).
type Term uint64

// Index is a position in the Raft log; the first entry has index 1 (§5.3).
type Index uint64
