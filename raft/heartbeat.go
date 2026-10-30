package raft

import (
	"context"
	"fmt"
)

func (rf *Raft) onHeartbeatTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.stoppedLocked() || rf.role != Leader {
		return
	}
	rf.heartbeatTimer.Reset(rf.heartbeatInterval)
	for _, p := range rf.peers {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// heartbeatPeer coalesces wakeups and keeps at most one heartbeat in flight
// per peer. A slow peer cannot block communication with the majority.
func (rf *Raft) heartbeatPeer(ctx context.Context, p peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}
		rf.mu.Lock()
		if rf.stoppedLocked() || rf.role != Leader {
			rf.mu.Unlock()
			continue
		}
		rpcCtx := rf.rpcContextLocked(ctx)
		args := &AppendEntriesArgs{Term: rf.currentTerm, LeaderID: rf.id}
		rf.mu.Unlock()
		rf.sendAppendEntries(rpcCtx, p.id, args)
	}
}

func (rf *Raft) sendAppendEntries(ctx context.Context, peer NodeID, args *AppendEntriesArgs) {
	ctx, cancel := rf.withTimeout(ctx, rf.appendEntriesTimeout)
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
	if rf.stoppedLocked() {
		return
	}
	if reply.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(reply.Term)
		return
	}
	if rf.role != Leader || rf.currentTerm != args.Term {
		return
	}
	rf.heardFrom[peer] = struct{}{}
}

func checkAppendEntriesReply(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	switch {
	case reply == nil:
		return errNilReply
	case reply.Term < args.Term:
		return fmt.Errorf("%w: heartbeat reply term below request", errInvalidReply)
	case reply.Success && reply.Term != args.Term:
		return fmt.Errorf("%w: heartbeat success in another term", errInvalidReply)
	case !reply.Success && reply.Term == args.Term:
		return fmt.Errorf("%w: heartbeat rejected in its own term", errInvalidReply)
	}
	return nil
}
