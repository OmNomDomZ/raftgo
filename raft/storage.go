package raft

// Storage persists a node's PersistentState. SaveState must be durable before
// it returns, and implementations must be safe for concurrent use.
type Storage interface {
	SaveState(state PersistentState) error
	// LoadState returns the zero PersistentState for a fresh node with no
	// saved state. Saved state that cannot be read must be reported as an
	// error, never as a fresh node: that would roll back currentTerm/votedFor.
	LoadState() (PersistentState, error)
}

// PersistentState is the term and vote that must survive a restart
// (Figure 2). The log is not part of it and will be persisted separately.
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
