package raft

import "context"

// Run drives the node until ctx is done, then waits for its in-flight RPCs to
// finish. It must be called exactly once, after New; until then the RPC
// handlers return ErrNotStarted. Once ctx is done the node is stopped for
// good: it reports itself as a non-leader and its RPC handlers return
// ErrStopped.
func (rf *Raft) Run(ctx context.Context) {
	rf.mu.Lock()
	if rf.started {
		rf.mu.Unlock()
		panic("raft: Run called twice")
	}
	rf.started = true
	rf.done = ctx.Done()
	rf.resetElectionTimerLocked()
	rf.logger.Info("started", "term", rf.currentTerm, "votedFor", rf.votedFor)
	rf.mu.Unlock()

	defer rf.wg.Wait()

	for _, p := range rf.peers {
		rf.wg.Go(func() { rf.replicate(ctx, p) })
	}
	rf.wg.Go(func() { rf.apply(ctx) })

	for {
		select {
		case <-ctx.Done():
			// Under mu, so no handler can re-arm a timer after they are stopped:
			// handlers see stoppedLocked once mu is released.
			rf.mu.Lock()
			rf.electionTimer.Stop()
			rf.heartbeatTimer.Stop()
			rf.cancelRPCsLocked()
			rf.logger.Info("stopped", "term", rf.currentTerm, "role", rf.role)
			rf.mu.Unlock()
			return
		case <-rf.electionTimer.C():
			rf.onElectionTimeout(ctx)
		case <-rf.heartbeatTimer.C():
			rf.onHeartbeatTimeout(ctx)
		}
	}
}
