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

// PersistentState is the state that must survive a restart (Figure 2).
type PersistentState struct {
	CurrentTerm Term
	VotedFor    NodeID // "" if no vote was cast in CurrentTerm
}

// persistLocked makes term and votedFor durable and only then adopts them in
// memory, so a failed write leaves the node's commitments unchanged: a
// restarted node must never vote twice in one term (§5.1).
func (rf *Raft) persistLocked(term Term, votedFor NodeID) error {
	err := rf.storage.SaveState(PersistentState{CurrentTerm: term, VotedFor: votedFor})
	if err != nil {
		rf.logger.Error("persist state failed", "term", term, "votedFor", votedFor, "err", err)
		return err
	}
	rf.currentTerm = term
	rf.votedFor = votedFor
	rf.dirty = false
	return nil
}

// syncLocked persists in-memory state that stepping down left non-durable.
// Callers must succeed here before sending a reply that commits the node to
// its current term.
func (rf *Raft) syncLocked() error {
	if !rf.dirty {
		return nil
	}
	return rf.persistLocked(rf.currentTerm, rf.votedFor)
}
