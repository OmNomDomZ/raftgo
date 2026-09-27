package raft

// Storage persists a node's PersistentState and log. Writes must be durable
// before they return, and implementations must be safe for concurrent use.
//
// TODO: specify failure semantics before the file-backed implementation. The
// node treats a failed write as transient and retries it later, but after a
// failed fsync the kernel may drop the dirty pages and a retried fsync can
// report success without the data on disk (Linux). An implementation must
// then fail every later call instead of letting a retry succeed, or the node
// could acknowledge entries or a vote a restart would lose.
type Storage interface {
	SaveState(state PersistentState) error
	// LoadState returns the zero PersistentState for a fresh node with no
	// saved state. Saved state that cannot be read must be reported as an
	// error, never as a fresh node: that would roll back currentTerm/votedFor.
	LoadState() (PersistentState, error)

	// StoreLog replaces the stored entries from index from on with entries,
	// as one atomic write: the node truncates a conflicting suffix and
	// appends in one call (§5.3). from is at least 1 and at most one past
	// the last stored index. StoreLog must not modify entries or keep
	// references into them after it returns.
	StoreLog(from Index, entries []LogEntry) error
	// LoadLog returns the stored entries, the first one at index 1. Like
	// LoadState, it must report a log it cannot read as an error, never as
	// an empty one: that would lose committed entries.
	LoadLog() ([]LogEntry, error)
}

// PersistentState is the term and vote that must survive a restart
// (Figure 2). The log is persisted separately, through Storage.StoreLog.
type PersistentState struct {
	CurrentTerm Term
	VotedFor    NodeID // "" if no vote was cast in CurrentTerm
}

func (rf *Raft) persistentStateLocked() PersistentState {
	return PersistentState{CurrentTerm: rf.currentTerm, VotedFor: rf.votedFor}
}

// persistLocked makes st durable and only then adopts it in memory, so a
// failed write leaves the node's commitments unchanged: a restarted node must
// never vote twice in one term (§5.1).
func (rf *Raft) persistLocked(st PersistentState) error {
	if err := rf.storage.SaveState(st); err != nil {
		rf.logger.Error("persist state failed",
			"term", st.CurrentTerm, "votedFor", st.VotedFor, "err", err)
		return err
	}
	rf.currentTerm = st.CurrentTerm
	rf.votedFor = st.VotedFor
	rf.dirty = false
	return nil
}

// syncLocked persists in-memory state that stepping down left non-durable.
// A reply that commits the node to its current term may only be sent once
// this succeeds; otherwise the handler returns the error instead of a reply.
func (rf *Raft) syncLocked() error {
	if !rf.dirty {
		return nil
	}
	return rf.persistLocked(rf.persistentStateLocked())
}
