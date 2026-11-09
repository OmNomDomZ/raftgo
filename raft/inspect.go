package raft

import "slices"

// Status — локальное состояние узла; votes значим только у Candidate.
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

// ElectionStatus содержит только поля выборов, без индексов лога.
type ElectionStatus struct {
	ID       NodeID `json:"id"`
	Role     string `json:"role"`
	Term     Term   `json:"term"`
	LeaderID NodeID `json:"leader_id"`
	VotedFor NodeID `json:"voted_for"`
	Votes    int    `json:"votes"`
	Quorum   int    `json:"quorum"`
	Running  bool   `json:"running"`
}

func (s Status) Election() ElectionStatus {
	return ElectionStatus{
		ID: s.ID, Role: s.Role, Term: s.Term, LeaderID: s.LeaderID,
		VotedFor: s.VotedFor, Votes: s.Votes, Quorum: s.Quorum, Running: s.Running,
	}
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

// Возвращаем независимую копию статуса и лога, снятых под одним mutex.
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
