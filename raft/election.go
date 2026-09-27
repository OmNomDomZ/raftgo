package raft

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

var (
	errNilReply     = errors.New("raft: transport returned a nil reply without an error")
	errInvalidReply = errors.New("raft: invalid reply")
)

func (rf *Raft) onElectionTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return
	}
	// Run may have received the tick while a handler held mu and re-armed the
	// timer, e.g. on a heartbeat from a live leader. The tick is then stale.
	if rf.clock.Now().Before(rf.electionDeadline) {
		return
	}
	if rf.role == Leader {
		rf.checkQuorumLocked()
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
	rf.leaderID = ""
	rf.votes = map[NodeID]struct{}{rf.id: {}}
	rf.cancelRPCsLocked()

	if len(rf.votes) >= rf.quorum() {
		rf.becomeLeaderLocked()
		return
	}

	ctx = rf.rpcContextLocked(ctx)
	args := &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateID:  rf.id,
		LastLogIndex: rf.lastLogIndexLocked(),
		LastLogTerm:  rf.lastLogTermLocked(),
	}
	for _, p := range rf.peers {
		rf.wg.Go(func() { rf.requestVote(ctx, p.id, args) })
	}
}

func (rf *Raft) requestVote(ctx context.Context, peer NodeID, args *RequestVoteArgs) {
	ctx, cancel := rf.withTimeout(ctx, rf.requestVoteTimeout)
	defer cancel()
	reply, err := rf.transport.RequestVote(ctx, peer, args)
	if err == nil {
		err = checkVoteReply(args, reply)
	}
	if err != nil {
		rf.logRPCFailure("RequestVote", peer, args.Term, err)
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

// logRPCFailure logs an outgoing RPC that got no usable reply. A lost
// message is routine, but a reply that breaks the protocol means the peer or
// the transport is buggy.
func (rf *Raft) logRPCFailure(rpc string, peer NodeID, term Term, err error) {
	level := slog.LevelDebug
	if errors.Is(err, errInvalidReply) || errors.Is(err, errNilReply) {
		level = slog.LevelError
	}
	rf.logger.Log(context.Background(), level, rpc+" failed", "peer", peer, "term", term, "err", err)
}

// checkVoteReply rejects replies no correct peer sends. A receiver adopts a
// higher request term before answering, so its reply term is never lower,
// and it only grants votes in the request's term (Figure 2).
func checkVoteReply(args *RequestVoteArgs, reply *RequestVoteReply) error {
	switch {
	case reply == nil:
		return errNilReply
	case reply.Term < args.Term:
		return fmt.Errorf("%w: RequestVote reply term %d below request term %d", errInvalidReply, reply.Term, args.Term)
	case reply.VoteGranted && reply.Term != args.Term:
		return fmt.Errorf("%w: vote granted in term %d for request term %d", errInvalidReply, reply.Term, args.Term)
	}
	return nil
}
