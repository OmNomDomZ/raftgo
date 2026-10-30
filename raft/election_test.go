package raft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRequestVoteReply_FromEarlierTermIsNotCounted(t *testing.T) {
	// A grant for term 2 that arrives while the node campaigns in term 3 was
	// not a vote for this election; counting it could elect two leaders in
	// term 3 (Election Safety).
	rf, _ := newTestNode(t, "A", "B", "C")
	rf.transport.(*fakeTransport).register("B", grantingPeer{})
	setRoleAndTerm(rf, Candidate, 3, "A")
	rf.votes = map[NodeID]struct{}{"A": {}}

	rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 2, CandidateID: "A"})
	if _, isLeader := rf.GetState(); isLeader {
		t.Fatalf("stale grant from term 2 made the node leader of term 3")
	}

	rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 3, CandidateID: "A"})
	if _, isLeader := rf.GetState(); !isLeader {
		t.Fatalf("setup: a grant for term 3 should complete the quorum")
	}
}

func TestRequestVoteReply_DuplicateGrantIsCountedOnce(t *testing.T) {
	// A transport that retries could deliver the same peer's grant twice;
	// counting it twice would let a candidate win without a real majority
	// (Election Safety).
	rf, _ := newTestNode(t, "A", "B", "C", "D", "E")
	tr := rf.transport.(*fakeTransport)
	tr.register("B", grantingPeer{})
	tr.register("C", grantingPeer{})
	setRoleAndTerm(rf, Candidate, 3, "A")
	rf.votes = map[NodeID]struct{}{"A": {}}
	args := &RequestVoteArgs{Term: 3, CandidateID: "A"}

	rf.requestVote(context.Background(), "B", args)
	rf.requestVote(context.Background(), "B", args)
	if _, isLeader := rf.GetState(); isLeader {
		t.Fatalf("A and a duplicated grant from B made a leader of a 5-node cluster")
	}

	rf.requestVote(context.Background(), "C", args)
	if _, isLeader := rf.GetState(); !isLeader {
		t.Fatalf("setup: grants from A, B and C should complete the quorum")
	}
}

func TestElectionTimeout_StaleTickAfterHeartbeatIsIgnored(t *testing.T) {
	// Run can receive the tick while a handler holds mu and re-arms the timer.
	rf, _ := newTestNode(t, "A", "B", "C")
	clock := rf.clock.(*fakeClock)
	setRoleAndTerm(rf, Follower, 1, "")
	rf.mu.Lock()
	rf.resetElectionTimerLocked()
	rf.mu.Unlock()

	clock.Advance(time.Second)
	<-rf.electionTimer.C() // Run took the tick but has not locked mu yet.
	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 1, LeaderID: "B"})
	rf.onElectionTimeout(context.Background())

	if role, term := roleAndTerm(rf); role != Follower || term != 1 {
		t.Fatalf("stale tick started an election against a live leader: %v in term %d", role, term)
	}

	clock.Advance(time.Second)
	<-rf.electionTimer.C()
	rf.onElectionTimeout(context.Background())
	if role, term := roleAndTerm(rf); role != Candidate || term != 2 {
		t.Fatalf("expected an election once the leader went silent, got %v in term %d", role, term)
	}
}

func TestReply_NilWithoutErrorIsAFailedRPC(t *testing.T) {
	cfg := testConfig(&fakeStorage{}, "A", "B", "C")
	cfg.Transport = nilTransport{}
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	setRoleAndTerm(rf, Candidate, 2, "A")

	rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 2, CandidateID: "A"})
	rf.sendAppendEntries(context.Background(), "B", &AppendEntriesArgs{Term: 2, LeaderID: "A"})

	if role, term := roleAndTerm(rf); role != Candidate || term != 2 {
		t.Fatalf("nil reply changed state: %v in term %d", role, term)
	}
}

func TestReply_WrongTermIsAFailedRPC(t *testing.T) {
	// A grant from another term is not a vote in this election, and a reply
	// that breaks Figure 2 must not move the node to its term either.
	for _, peerTerm := range []Term{1, 3} {
		t.Run(fmt.Sprintf("peer term %d", peerTerm), func(t *testing.T) {
			rf, _ := newTestNode(t, "A", "B", "C")
			rf.transport.(*fakeTransport).register("B", wrongTermPeer{term: peerTerm})
			setRoleAndTerm(rf, Candidate, 2, "A")
			rf.votes = map[NodeID]struct{}{"A": {}}

			rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 2, CandidateID: "A"})
			if role, term := roleAndTerm(rf); role != Candidate || term != 2 {
				t.Fatalf("invalid RequestVote reply changed state: %v in term %d", role, term)
			}

			setRoleAndTerm(rf, Leader, 2, "A")
			rf.sendAppendEntries(context.Background(), "B", &AppendEntriesArgs{Term: 2, LeaderID: "A"})
			if role, term := roleAndTerm(rf); role != Leader || term != 2 {
				t.Fatalf("invalid AppendEntries reply changed state: %v in term %d", role, term)
			}
		})
	}
}

func TestReply_InvalidIsLoggedAsError(t *testing.T) {
	// A lost message is routine; a reply that breaks the protocol is a bug in
	// the peer or the transport and must not be hidden at debug level.
	var logs bytes.Buffer
	cfg := testConfig(&fakeStorage{}, "A", "B", "C")
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	setRoleAndTerm(rf, Candidate, 2, "A")

	// C is not registered, so the request is lost.
	rf.requestVote(context.Background(), "C", &RequestVoteArgs{Term: 2, CandidateID: "A"})
	rf.sendAppendEntries(context.Background(), "C", &AppendEntriesArgs{Term: 2, LeaderID: "A"})
	if logs.Len() != 0 {
		t.Fatalf("lost messages logged above debug level: %s", logs.String())
	}

	rf.transport.(*fakeTransport).register("B", wrongTermPeer{term: 1})
	rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 2, CandidateID: "A"})
	rf.sendAppendEntries(context.Background(), "B", &AppendEntriesArgs{Term: 2, LeaderID: "A"})
	for _, rpc := range []string{"RequestVote", "AppendEntries"} {
		if !strings.Contains(logs.String(), "level=ERROR msg=\""+rpc+" failed\"") {
			t.Fatalf("invalid %s reply not logged as an error: %s", rpc, logs.String())
		}
	}
}

func TestStartElection_PersistFailureDoesNotCampaign(t *testing.T) {
	// A candidate must have durably voted for itself in its new term, or a
	// restart could let it vote for another node in that term (§5.1).
	synctest.Test(t, func(t *testing.T) {
		rf, st := newTestNode(t, "A", "B", "C")
		defer rf.wg.Wait()
		setRoleAndTerm(rf, Follower, 2, "")
		st.setFailSave(true)

		rf.mu.Lock()
		rf.startElectionLocked(context.Background())
		rf.mu.Unlock()
		if role, term := roleAndTerm(rf); role != Follower || term != 2 || votedFor(rf) != "" {
			t.Fatalf("non-durable election changed state: %v in term %d, votedFor %q", role, term, votedFor(rf))
		}
		if !rf.electionTimer.Stop() {
			t.Fatalf("election timer not armed, so the election is never retried")
		}

		st.setFailSave(false)
		rf.mu.Lock()
		rf.startElectionLocked(context.Background())
		rf.mu.Unlock()
		if role, term := roleAndTerm(rf); role != Candidate || term != 3 || votedFor(rf) != "A" {
			t.Fatalf("expected a candidate of term 3 once storage recovers, got %v in term %d, votedFor %q",
				role, term, votedFor(rf))
		}
		if state, _ := st.LoadState(); state.CurrentTerm != 3 || state.VotedFor != "A" {
			t.Fatalf("self-vote not persisted: %+v", state)
		}
	})
}

func TestNotStarted_NodeAnswersNothing(t *testing.T) {
	st := &fakeStorage{}
	rf, err := New(testConfig(st, "A", "B", "C"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if reply, err := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"}); !errors.Is(err, ErrNotStarted) || reply != nil {
		t.Fatalf("node answered RequestVote before Run: reply=%+v err=%v", reply, err)
	}
	if reply, err := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 1, LeaderID: "B"}); !errors.Is(err, ErrNotStarted) || reply != nil {
		t.Fatalf("node answered AppendEntries before Run: reply=%+v err=%v", reply, err)
	}
	if role, term := roleAndTerm(rf); role != Follower || term != 0 || st.saveCount() != 0 {
		t.Fatalf("node changed state before Run: %v in term %d, %d saves", role, term, st.saveCount())
	}
	if rf.electionTimer.Stop() {
		t.Fatalf("node armed its election timer before Run")
	}
}

func stoppedNode(t *testing.T) (*Raft, *fakeStorage) {
	t.Helper()
	st := &fakeStorage{}
	rf, err := New(testConfig(st, "A", "B", "C"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rf.Run(ctx)
	return rf, st
}

func TestStopped_NodeIsNotLeaderAndChangesNothing(t *testing.T) {
	rf, st := stoppedNode(t)
	setRoleAndTerm(rf, Leader, 2, "A")

	if _, isLeader := rf.GetState(); isLeader {
		t.Fatalf("stopped node reports itself as leader")
	}
	// A stopped node answers nothing: Success=false would claim a log
	// mismatch (§5.3).
	if reply, err := rf.HandleRequestVote(&RequestVoteArgs{Term: 5, CandidateID: "B"}); !errors.Is(err, ErrStopped) || reply != nil {
		t.Fatalf("stopped node answered RequestVote: reply=%+v err=%v", reply, err)
	}
	if reply, err := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 5, LeaderID: "B"}); !errors.Is(err, ErrStopped) || reply != nil {
		t.Fatalf("stopped node answered AppendEntries: reply=%+v err=%v", reply, err)
	}
	if role, term := roleAndTerm(rf); role != Leader || term != 2 {
		t.Fatalf("stopped node changed state: %v in term %d", role, term)
	}
	if n := st.saveCount(); n != 0 {
		t.Fatalf("stopped node wrote storage %d times", n)
	}
	if rf.electionTimer.Stop() || rf.heartbeatTimer.Stop() {
		t.Fatalf("stopped node has an armed timer")
	}
}

func TestRun_CalledTwicePanics(t *testing.T) {
	rf, _ := stoppedNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected a second Run to panic")
		} else if _, ok := r.(fatalError); ok {
			t.Fatalf("misuse of Run must panic, not report a protocol violation: %v", r)
		}
	}()
	rf.Run(ctx)
}

func TestFatal_OnFatalReturningStillStopsTheNode(t *testing.T) {
	cfg := testConfig(&fakeStorage{}, "A", "B")
	var called bool
	cfg.OnFatal = func(string) { called = true }
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	setRoleAndTerm(rf, Follower, 3, "")

	defer func() {
		if recover() == nil {
			t.Fatalf("node carried on after OnFatal returned")
		}
		if !called {
			t.Fatalf("OnFatal was not called")
		}
	}()
	rf.adoptHigherTermLocked(3)
}

// blockedNode returns a node whose RPCs hang until cancelled. RequestVoteTimeout is
// far beyond anything a test waits for, so a released RequestVote was
// cancelled; a heartbeat also times out after HeartbeatInterval.
func blockedNode(t *testing.T) (*Raft, blockingTransport) {
	t.Helper()
	tr := newBlockingTransport()
	cfg := testConfig(&fakeStorage{}, "A", "B", "C")
	cfg.Transport = tr
	cfg.RequestVoteTimeout = time.Hour
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rf, tr
}

// releasedTerms returns the terms of the RPCs released so far.
func releasedTerms(tr blockingTransport) []Term {
	var terms []Term
	for {
		select {
		case term := <-tr.released:
			terms = append(terms, term)
		default:
			return terms
		}
	}
}

func TestRPCs_NewElectionCancelsPreviousTermVotes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf, tr := blockedNode(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer rf.wg.Wait()
		defer cancel()

		rf.mu.Lock()
		rf.startElectionLocked(ctx)
		rf.mu.Unlock()
		synctest.Wait()
		if got := releasedTerms(tr); len(got) != 0 {
			t.Fatalf("vote requests released before any change: %v", got)
		}

		rf.mu.Lock()
		rf.startElectionLocked(ctx)
		rf.mu.Unlock()
		synctest.Wait()
		if got := releasedTerms(tr); !slices.Equal(got, []Term{1, 1}) {
			t.Fatalf("expected both term-1 vote requests cancelled, got %v", got)
		}
	})
}

func TestRPCs_SteppingDownCancelsHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf, tr := blockedNode(t)
		ctx, stop := runNode(rf)
		defer stop()
		setRoleAndTerm(rf, Leader, 2, "A")

		rf.onHeartbeatTimeout(ctx)
		synctest.Wait()
		if got := releasedTerms(tr); len(got) != 0 {
			t.Fatalf("heartbeats released while still leader: %v", got)
		}

		mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 3, LeaderID: "B"})
		synctest.Wait()
		if got := releasedTerms(tr); !slices.Equal(got, []Term{2, 2}) {
			t.Fatalf("expected both term-2 heartbeats cancelled, got %v", got)
		}
	})
}

func TestRPCs_BecomingLeaderCancelsOutstandingVotes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rf, tr := blockedNode(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer rf.wg.Wait()
		defer cancel()

		rf.mu.Lock()
		rf.startElectionLocked(ctx)
		rf.votes["B"] = struct{}{} // as if B's grant arrived through another path
		rf.becomeLeaderLocked()
		rf.mu.Unlock()
		synctest.Wait()
		if got := releasedTerms(tr); !slices.Equal(got, []Term{1, 1}) {
			t.Fatalf("expected both term-1 vote requests cancelled, got %v", got)
		}
	})
}

func TestHeartbeats_AtMostOneInFlightPerPeer(t *testing.T) {
	// Heartbeats to a slow peer must not pile up: in 3B, concurrent
	// AppendEntries to one peer could arrive out of order.
	synctest.Test(t, func(t *testing.T) {
		rf, tr := blockedNode(t)
		ctx, stop := runNode(rf)
		defer stop()
		setRoleAndTerm(rf, Leader, 2, "A")

		for range 3 {
			rf.onHeartbeatTimeout(ctx)
			synctest.Wait()
		}
		mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 3, LeaderID: "B"})
		synctest.Wait()
		if got := releasedTerms(tr); !slices.Equal(got, []Term{2, 2}) {
			t.Fatalf("expected one term-2 heartbeat per peer, got %v", got)
		}
	})
}

func TestHeartbeats_HungRPCTimesOutAfterAppendEntriesTimeout(t *testing.T) {
	// A lost heartbeat must not hold back the next one for longer than
	// AppendEntriesTimeout, or the peer's election timer fires under a
	// healthy leader. The deadline follows the node's Clock, not wall time.
	synctest.Test(t, func(t *testing.T) {
		rf, tr := blockedNode(t)
		ctx, stop := runNode(rf)
		defer stop()
		setRoleAndTerm(rf, Leader, 2, "A")

		clock := rf.clock.(*fakeClock)
		rf.onHeartbeatTimeout(ctx)
		synctest.Wait()
		clock.Advance(rf.appendEntriesTimeout - time.Nanosecond)
		synctest.Wait()
		if got := releasedTerms(tr); len(got) != 0 {
			t.Fatalf("hung heartbeats released before AppendEntriesTimeout: %v", got)
		}
		clock.Advance(time.Nanosecond)
		synctest.Wait()
		if got := releasedTerms(tr); !slices.Equal(got, []Term{2, 2}) {
			t.Fatalf("expected both hung heartbeats released after AppendEntriesTimeout, got %v", got)
		}

		// The next heartbeat goes out and times out the same way.
		rf.onHeartbeatTimeout(ctx)
		synctest.Wait()
		clock.Advance(rf.appendEntriesTimeout)
		synctest.Wait()
		if got := releasedTerms(tr); len(got) != 2 {
			t.Fatalf("expected the next heartbeats to go out and time out, got %v", got)
		}
	})
}
