package raft

import "context"

// Run drives the node until ctx is done, then waits for its in-flight RPCs to
// finish. It must be called exactly once, after New.
func (rf *Raft) Run(ctx context.Context) {
	defer rf.wg.Wait()

	rf.mu.Lock()
	if rf.role != Leader {
		rf.electionTimer.Reset(rf.electionTimeout())
	}
	rf.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			rf.electionTimer.Stop()
			rf.heartbeatTimer.Stop()
			return
		case <-rf.electionTimer.C():
			rf.onElectionTimeout(ctx)
		case <-rf.heartbeatTimer.C():
			rf.onHeartbeatTimeout(ctx)
		}
	}
}
