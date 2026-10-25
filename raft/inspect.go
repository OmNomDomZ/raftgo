package raft

import "slices"

// Status is a local observation, not proof of current leadership.
type Status struct {
	ID           NodeID `json:"id"`
	Role         string `json:"role"`
	Term         Term   `json:"term"`
	LeaderID     NodeID `json:"leader_id"`
	VotedFor     NodeID `json:"voted_for"`
	Votes        int    `json:"votes"`
	Quorum       int    `json:"quorum"`
	CommitIndex  Index  `json:"commit_index"`
	LastLogIndex Index  `json:"last_log_index"`
	Running      bool   `json:"running"`
}

func (rf *Raft) statusLocked() Status {
	votes := 0
	if rf.role == Candidate {
		votes = len(rf.votes)
	}
	return Status{rf.id, rf.role.String(), rf.currentTerm, rf.leaderLocked(), rf.votedFor, votes, rf.quorum(), rf.commitIndex, rf.lastLogIndexLocked(), rf.started && !rf.stoppedLocked()}
}

func (rf *Raft) Status() Status {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.statusLocked()
}

// InspectLog returns an atomic local snapshot. Uncommitted entries may change.
// Both the slice and command bytes are owned by the caller.
func (rf *Raft) InspectLog() (Status, []LogEntry) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	entries := make([]LogEntry, len(rf.log)-1)
	for i, e := range rf.log[1:] {
		entries[i] = e
		entries[i].Command = slices.Clone(e.Command)
	}
	return rf.statusLocked(), entries
}
