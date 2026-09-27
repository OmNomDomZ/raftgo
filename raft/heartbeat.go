package raft

import (
	"context"
	"fmt"
)

func (rf *Raft) onHeartbeatTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// A tick that raced with stepping down. A stale tick while still Leader
	// only sends one extra heartbeat, which is harmless.
	if rf.stoppedLocked() || rf.role != Leader {
		return
	}
	rf.heartbeatTimer.Reset(rf.heartbeatInterval)

	for _, p := range rf.peers {
		select {
		case p.wake <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
}

// replicate runs for the node's lifetime and sends p a heartbeat on every
// wake-up while this node is Leader. Keeping at most one AppendEntries in
// flight per peer means a slow peer coalesces wake-ups instead of piling up
// goroutines and receiving RPCs out of order.
func (rf *Raft) replicate(ctx context.Context, p peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}

		rf.mu.Lock()
		// A wake-up can outlive the leadership it was sent for.
		if rf.stoppedLocked() || rf.role != Leader {
			rf.mu.Unlock()
			continue
		}
		rpcCtx := rf.rpcContextLocked(ctx)
		args := &AppendEntriesArgs{Term: rf.currentTerm, LeaderID: rf.id}
		rf.mu.Unlock()

		rf.sendHeartbeat(rpcCtx, p.id, args)
	}
}

func (rf *Raft) sendHeartbeat(ctx context.Context, peer NodeID, args *AppendEntriesArgs) {
	// A heartbeat is stale once the next one is due. Waiting longer on a lost
	// one would hold back the next and let the peer's election timer fire.
	ctx, cancel := context.WithTimeout(ctx, rf.heartbeatInterval)
	defer cancel()
	reply, err := rf.transport.AppendEntries(ctx, peer, args)
	if err == nil {
		err = checkAppendEntriesReply(args, reply)
	}
	if err != nil {
		rf.logRPCFailure("AppendEntries", peer, args.Term, err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if !rf.stoppedLocked() && reply.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(reply.Term)
	}
}

// checkAppendEntriesReply rejects replies no correct peer sends; see
// checkVoteReply.
func checkAppendEntriesReply(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	switch {
	case reply == nil:
		return errNilReply
	case reply.Term < args.Term:
		return fmt.Errorf("%w: AppendEntries reply term %d below request term %d", errInvalidReply, reply.Term, args.Term)
	case reply.Success && reply.Term != args.Term:
		return fmt.Errorf("%w: success in term %d for request term %d", errInvalidReply, reply.Term, args.Term)
	}
	return nil
}
