package raft

// NodeID identifies a cluster member.
type NodeID string

// Term is a Raft term number (§5.1).
type Term uint64

// Index is retained only for the standard zero RequestVote log field.
type Index uint64
