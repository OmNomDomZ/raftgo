package raft

import (
	"context"
	"slices"
)

// pendingRead is a ReadIndex call waiting until it is safe to answer.
type pendingRead struct {
	// round is the read round a quorum must answer; see Raft.readRound.
	round uint64
	// index is the commit index the read may be served at, or 0 until this
	// leader has committed an entry of its own term.
	index Index
	// done receives nil once index is confirmed, or why it never will be.
	done chan error
}

// ReadIndex returns an index such that, once the state machine has applied
// every entry through it, it reflects every write that completed before
// ReadIndex was called. Serving a read from that state is linearizable
// without appending the read to the log (§6.4 of Ongaro's thesis).
//
// It returns a *NotLeaderError if this node is not the leader or stops being
// it before the index is confirmed, and ctx.Err() if ctx is done first.
func (rf *Raft) ReadIndex(ctx context.Context) (Index, error) {
	rf.mu.Lock()
	if err := rf.runningLocked(); err != nil {
		rf.mu.Unlock()
		return 0, err
	}
	if rf.role != Leader {
		err := rf.notLeaderErrLocked()
		rf.mu.Unlock()
		return 0, err
	}
	// Answers to AppendEntries sent from now on show that a quorum still
	// knows no later term, so this node was still the leader after the call
	// began. A leader cut off in a minority never gets them.
	rf.readRound++
	r := &pendingRead{round: rf.readRound, done: make(chan error, 1)}
	rf.reads = append(rf.reads, r)
	rf.resolveReadsLocked()
	rf.wakeReplicatorsLocked()
	stopped := rf.done
	rf.mu.Unlock()

	select {
	case err := <-r.done:
		if err != nil {
			return 0, err
		}
		// Written under mu before done was sent.
		return r.index, nil
	case <-ctx.Done():
		rf.mu.Lock()
		rf.reads = slices.DeleteFunc(rf.reads, func(p *pendingRead) bool { return p == r })
		rf.mu.Unlock()
		return 0, ctx.Err()
	case <-stopped:
		return 0, ErrStopped
	}
}

// resolveReadsLocked answers every pending read that is now safe to serve.
func (rf *Raft) resolveReadsLocked() {
	rf.reads = slices.DeleteFunc(rf.reads, func(r *pendingRead) bool {
		// A new leader's commitIndex may lag the true one until it commits an
		// entry of its own term, which its no-op does (§6.4, §8). Leader
		// Completeness then puts every earlier committed write at or below it.
		if r.index == 0 && rf.termAtLocked(rf.commitIndex) == rf.currentTerm {
			r.index = rf.commitIndex
		}
		if r.index == 0 || !rf.roundConfirmedLocked(r.round) {
			return false
		}
		r.done <- nil
		return true
	})
}

// roundConfirmedLocked reports whether a quorum, counting this node, has
// answered an AppendEntries of the given read round or a later one.
func (rf *Raft) roundConfirmedLocked(round uint64) bool {
	acks := 1
	for _, p := range rf.peers {
		if rf.ackedRound[p.id] >= round {
			acks++
		}
	}
	return acks >= rf.quorum()
}

// failReadsLocked fails every pending read with err.
func (rf *Raft) failReadsLocked(err error) {
	for _, r := range rf.reads {
		r.done <- err
	}
	rf.reads = nil
}
