package raft

// Status is a local observation, not proof of current leadership.
type Status struct {
	ID       NodeID `json:"id"`
	Role     string `json:"role"`
	Term     Term   `json:"term"`
	LeaderID NodeID `json:"leader_id"`
	VotedFor NodeID `json:"voted_for"`
	Votes    int    `json:"votes"`
	Quorum   int    `json:"quorum"`
	Running  bool   `json:"running"`
}

func (rf *Raft) statusLocked() Status {
	votes := 0
	if rf.role == Candidate {
		votes = len(rf.votes)
	}
	return Status{rf.id, rf.role.String(), rf.currentTerm, rf.leaderLocked(), rf.votedFor, votes, rf.quorum(), rf.started && !rf.stoppedLocked()}
}

func (rf *Raft) Status() Status {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.statusLocked()
}
