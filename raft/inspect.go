package raft

import "slices"

// Status is a local observation, not proof of current leadership.
// VotedFor is this node's vote in Term, or "" if it has not voted.
// Votes counts distinct grants (including self) only while Candidate;
// zero for other roles does not describe their election history.
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

// ElectionStatus is the election-only diagnostic schema for the lab1 snapshot.
// Keep log diagnostics in Status in main; extracting lab1 must also remove the
// log implementation and routes, not just project their fields away.
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

// Election projects the same local observation without log fields.
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
