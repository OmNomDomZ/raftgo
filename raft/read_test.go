package raft

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

type readResult struct {
	index Index
	err   error
}

// startRead calls rf.ReadIndex in the background; the result arrives on the
// returned channel.
func startRead(ctx context.Context, rf *Raft) <-chan readResult {
	res := make(chan readResult, 1)
	go func() {
		index, err := rf.ReadIndex(ctx)
		res <- readResult{index, err}
	}()
	return res
}

// mustBePending fails the test if the read has already returned.
func mustBePending(t *testing.T, res <-chan readResult, why string) {
	t.Helper()
	synctest.Wait()
	select {
	case r := <-res:
		t.Fatalf("ReadIndex returned %d, %v %s", r.index, r.err, why)
	default:
	}
}

func pendingReads(rf *Raft) int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return len(rf.reads)
}

func TestReadIndex_WaitsForQuorumAndOwnTermCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Index 1 is committed, but it is from term 1: a new leader's
		// commitIndex may lag until it commits an entry of its own term.
		rf := newTestLeader(t, 2, 1, entriesOfTerms(1)...)

		res := startRead(context.Background(), rf)
		mustBePending(t, res, "before any peer answered")

		replicateTo(rf, "B")
		mustBePending(t, res, "before the leader committed an entry of its term")

		index, _, err := rf.Start([]byte("x"))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		replicateTo(rf, "B")
		if r := <-res; r.err != nil || r.index != index {
			t.Fatalf("ReadIndex = %d, %v; want %d once entry %d of term 2 is committed", r.index, r.err, index, index)
		}
	})
}

func TestReadIndex_AnswerToAnEarlierRequestDoesNotConfirm(t *testing.T) {
	// Only an AppendEntries sent after ReadIndex began shows that no later
	// leader existed by then; an answer to one sent before shows nothing.
	synctest.Test(t, func(t *testing.T) {
		rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)

		rf.mu.Lock()
		early, earlyRound := rf.appendEntriesArgsLocked("B")
		rf.mu.Unlock()
		res := startRead(context.Background(), rf)
		mustBePending(t, res, "without any answer")

		rf.sendAppendEntries(context.Background(), "B", early, earlyRound)
		mustBePending(t, res, "on an answer to a request sent before the call")

		replicateTo(rf, "B")
		if r := <-res; r.err != nil || r.index != 1 {
			t.Fatalf("ReadIndex = %d, %v; want 1", r.index, r.err)
		}
	})
}

func TestReadIndex_OnlyTheLeaderServesReads(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	if _, err := rf.ReadIndex(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("follower: ReadIndex error %v, want ErrNotLeader", err)
	}
}

func TestReadIndex_SteppingDownFailsPendingReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
		res := startRead(context.Background(), rf)
		mustBePending(t, res, "without any answer")

		mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 3, LeaderID: "B"})
		if r := <-res; !errors.Is(r.err, ErrNotLeader) {
			t.Fatalf("ReadIndex = %d, %v after stepping down; want ErrNotLeader", r.index, r.err)
		}
		if n := pendingReads(rf); n != 0 {
			t.Fatalf("%d reads still pending after stepping down", n)
		}
	})
}

func TestReadIndex_CancelledReadIsForgotten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
		ctx, cancel := context.WithCancel(context.Background())
		res := startRead(ctx, rf)
		mustBePending(t, res, "without any answer")

		cancel()
		if r := <-res; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("ReadIndex = %d, %v after cancel; want context.Canceled", r.index, r.err)
		}
		if n := pendingReads(rf); n != 0 {
			t.Fatalf("%d reads still pending after cancel", n)
		}
	})
}

func TestReadIndex_SingleNodeConfirmsAtOnce(t *testing.T) {
	rf, _ := newTestNode(t, "A")
	setLog(t, rf, entriesOfTerms(1)...)
	setRoleAndTerm(rf, Leader, 1, "A")
	rf.mu.Lock()
	rf.commitIndex = 1
	rf.mu.Unlock()

	if index, err := rf.ReadIndex(context.Background()); err != nil || index != 1 {
		t.Fatalf("ReadIndex = %d, %v; want 1", index, err)
	}
}

func TestReadIndex_QuorumLossFailsPendingReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
		res := startRead(context.Background(), rf)
		mustBePending(t, res, "without any answer")

		rf.mu.Lock()
		rf.checkQuorumLocked()
		rf.mu.Unlock()
		if r := <-res; !errors.Is(r.err, ErrNotLeader) {
			t.Fatalf("ReadIndex = %d, %v after losing the quorum; want ErrNotLeader", r.index, r.err)
		}
	})
}

func TestReadIndex_StoppedNode(t *testing.T) {
	rf, _ := stoppedNode(t)
	if _, err := rf.ReadIndex(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopped node: ReadIndex error %v, want ErrStopped", err)
	}
}

// readIndex calls rf.ReadIndex while the cluster keeps running, since
// confirming leadership takes a round of AppendEntries.
func (c *testCluster) readIndex(t *testing.T, rf *Raft) (Index, error) {
	t.Helper()
	res := startRead(context.Background(), rf)
	var r readResult
	c.advanceUntil(t, 5*time.Second, func() bool {
		select {
		case r = <-res:
			return true
		default:
			return false
		}
	})
	return r.index, r.err
}

func TestCluster_ReadIndexCoversCompletedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.waitLeader(t)
		written := c.one(t, "a", 3)

		leader := c.waitLeader(t)
		index, err := c.readIndex(t, leader)
		if err != nil {
			t.Fatalf("ReadIndex on a healthy leader: %v", err)
		}
		if index < written {
			t.Fatalf("ReadIndex %d misses write at %d that completed before the read", index, written)
		}
	})
}

func TestCluster_CutOffLeaderCannotServeReads(t *testing.T) {
	// The cut-off leader believes it leads while the majority elects a new
	// leader and commits a write it never sees. A read served by the old
	// leader would miss that write, so the read must fail instead.
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		old := c.waitLeader(t)
		c.one(t, "a", 3)

		c.tr.setPartitioned(old.id, true)
		res := startRead(context.Background(), old)
		c.advanceUntil(t, 5*time.Second, func() bool { return c.leaderOtherThan(old) != nil })
		c.one(t, "b", 2)

		c.advanceUntil(t, 5*time.Second, func() bool { return len(res) == 1 })
		if r := <-res; !errors.Is(r.err, ErrNotLeader) {
			t.Fatalf("cut-off leader: ReadIndex = %d, %v; want ErrNotLeader", r.index, r.err)
		}
	})
}
