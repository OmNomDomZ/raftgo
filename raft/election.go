package raft

import (
	"context"
	"errors"
)

var errNilReply = errors.New("raft: transport returned a nil reply without an error")

func (rf *Raft) onElectionTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() || rf.role == Leader {
		return
	}
	// Run may have received the tick while a handler held mu and re-armed the
	// timer, e.g. on a heartbeat from a live leader. The tick is then stale.
	if rf.clock.Now().Before(rf.electionDeadline) {
		return
	}
	rf.startElectionLocked(ctx)
}

// startElectionLocked starts an election for currentTerm+1 (§5.2).
func (rf *Raft) startElectionLocked(ctx context.Context) {
	// Reset before persisting: if persistence fails, the timer must still be
	// armed so the election is retried later.
	rf.resetElectionTimerLocked()

	st := rf.persistentStateLocked()
	st.CurrentTerm++
	st.VotedFor = rf.id
	if err := rf.persistLocked(st); err != nil {
		return
	}
	rf.logger.Info("became candidate", "term", rf.currentTerm, "oldRole", rf.role)
	rf.role = Candidate
	rf.votes = map[NodeID]struct{}{rf.id: {}}
	rf.cancelRPCsLocked()

	if len(rf.votes) >= rf.quorum() {
		rf.becomeLeaderLocked()
		return
	}

	ctx = rf.rpcContextLocked(ctx)
	args := &RequestVoteArgs{Term: rf.currentTerm, CandidateID: rf.id}
	for _, p := range rf.peers {
		rf.wg.Go(func() { rf.requestVote(ctx, p.id, args) })
	}
}

func (rf *Raft) requestVote(ctx context.Context, peer NodeID, args *RequestVoteArgs) {
	ctx, cancel := context.WithTimeout(ctx, rf.rpcTimeout)
	defer cancel()
	reply, err := rf.transport.RequestVote(ctx, peer, args)
	if err == nil && reply == nil {
		err = errNilReply
	}
	if err != nil {
		rf.logger.Debug("RequestVote failed", "peer", peer, "term", args.Term, "err", err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return
	}
	if reply.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(reply.Term)
		return
	}
	if rf.role != Candidate || rf.currentTerm != args.Term || !reply.VoteGranted {
		return
	}

	rf.votes[peer] = struct{}{}
	if len(rf.votes) >= rf.quorum() {
		rf.becomeLeaderLocked()
	}
}
