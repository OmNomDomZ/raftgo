package raft

import "context"

func (rf *Raft) onElectionTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.role == Leader {
		return
	}
	rf.startElectionLocked(ctx)
}

// startElectionLocked starts an election for currentTerm+1 (§5.2).
func (rf *Raft) startElectionLocked(ctx context.Context) {
	// Reset before persisting: if persistence fails, the timer must still be
	// armed so the election is retried later.
	rf.electionTimer.Reset(rf.electionTimeout())

	newTerm := rf.currentTerm + 1
	if err := rf.persistLocked(newTerm, rf.id); err != nil {
		return
	}
	rf.role = Candidate
	rf.votesReceived = 1

	if rf.votesReceived >= rf.quorum() {
		rf.becomeLeaderLocked()
		return
	}

	args := &RequestVoteArgs{Term: newTerm, CandidateID: rf.id}
	for _, peer := range rf.peers {
		rf.wg.Go(func() { rf.requestVote(ctx, peer, args) })
	}
}

func (rf *Raft) requestVote(ctx context.Context, peer NodeID, args *RequestVoteArgs) {
	ctx, cancel := context.WithTimeout(ctx, rf.rpcTimeout)
	defer cancel()
	reply, err := rf.transport.RequestVote(ctx, peer, args)
	if err != nil {
		rf.logger.Debug("RequestVote failed", "peer", peer, "term", args.Term, "err", err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if reply.Term > rf.currentTerm {
		rf.becomeFollowerLocked(reply.Term)
		return
	}
	if rf.role != Candidate || rf.currentTerm != args.Term || !reply.VoteGranted {
		return
	}

	rf.votesReceived++
	if rf.votesReceived >= rf.quorum() {
		rf.becomeLeaderLocked()
	}
}
