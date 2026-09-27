package raft

var _ RPCHandler = (*Raft)(nil)

// HandleRequestVote implements the RequestVote RPC receiver (Figure 2).
func (rf *Raft) HandleRequestVote(args *RequestVoteArgs) *RequestVoteReply {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term < rf.currentTerm {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}
	}
	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// TODO: also require the candidate's log to be at least as up-to-date
	// as ours (§5.4.1).
	if rf.votedFor != "" && rf.votedFor != args.CandidateID {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}
	}

	// Also makes a term adopted by stepping down durable.
	if err := rf.persistLocked(rf.currentTerm, args.CandidateID); err != nil {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}
	}
	rf.electionTimer.Reset(rf.electionTimeout())
	return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: true}
}

// HandleAppendEntries implements the AppendEntries RPC receiver (Figure 2).
func (rf *Raft) HandleAppendEntries(args *AppendEntriesArgs) *AppendEntriesReply {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term < rf.currentTerm {
		return &AppendEntriesReply{Term: rf.currentTerm, Success: false}
	}
	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	} else if rf.role == Candidate {
		// Another node already won this term's election (§5.2).
		rf.role = Follower
	}
	// The leader is legitimate whether or not we can persist, so there is no
	// reason to start an election against it.
	rf.electionTimer.Reset(rf.electionTimeout())

	// Success counts toward the leader's commit quorum. If our term is not
	// durable, a restart could roll it back and let a stale leader overwrite
	// entries we acknowledged (§5.4).
	if err := rf.syncLocked(); err != nil {
		return &AppendEntriesReply{Term: rf.currentTerm, Success: false}
	}

	// TODO: log consistency check and entry append (§5.3).
	return &AppendEntriesReply{Term: rf.currentTerm, Success: true}
}
