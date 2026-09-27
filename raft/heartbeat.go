package raft

import "context"

func (rf *Raft) onHeartbeatTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// A tick that raced with stepping down.
	if rf.role != Leader {
		return
	}
	rf.heartbeatTimer.Reset(rf.heartbeatInterval)

	args := &AppendEntriesArgs{Term: rf.currentTerm, LeaderID: rf.id}
	for _, peer := range rf.peers {
		rf.wg.Go(func() { rf.sendHeartbeat(ctx, peer, args) })
	}
}

func (rf *Raft) sendHeartbeat(ctx context.Context, peer NodeID, args *AppendEntriesArgs) {
	ctx, cancel := context.WithTimeout(ctx, rf.rpcTimeout)
	defer cancel()
	reply, err := rf.transport.AppendEntries(ctx, peer, args)
	if err != nil {
		rf.logger.Debug("AppendEntries failed", "peer", peer, "term", args.Term, "err", err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if reply.Term > rf.currentTerm {
		rf.becomeFollowerLocked(reply.Term)
	}
}
