package raft

import (
	"context"
	"testing"
)

// These tests cover stepping down while storage is failing: the node must
// adopt the higher term in memory, but must not promise anything based on it
// until it is durable (§5.1).

func TestRequestVoteReply_HigherTermStepsDownDespitePersistFailure(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	rf.transport.(*fakeTransport).register("B", stubPeer{term: 5})
	setRoleAndTerm(rf, Candidate, 2, "A")
	st.setFailSave(true)

	rf.requestVote(context.Background(), "B", &RequestVoteArgs{Term: 2, CandidateID: "A"})

	if role, term := roleAndTerm(rf); role != Follower || term != 5 {
		t.Fatalf("expected Follower at term 5, got %v at term %d", role, term)
	}
}

func TestHeartbeatReply_HigherTermStepsDownDespitePersistFailure(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	rf.transport.(*fakeTransport).register("B", stubPeer{term: 5})
	setRoleAndTerm(rf, Leader, 2, "A")
	rf.heartbeatTimer.Reset(0)
	st.setFailSave(true)

	rf.sendAppendEntries(context.Background(), "B", &AppendEntriesArgs{Term: 2, LeaderID: "A"})

	if role, term := roleAndTerm(rf); role != Follower || term != 5 {
		t.Fatalf("expected Follower at term 5, got %v at term %d", role, term)
	}
	if rf.heartbeatTimer.Stop() {
		t.Fatalf("former leader must stop sending heartbeats")
	}
	if !rf.electionTimer.Stop() {
		t.Fatalf("former leader must arm its election timer")
	}
}

func TestHandleRequestVote_NonDurableTermGetsNoReplyUntilStorageRecovers(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Candidate, 2, "A")
	st.setFailSave(true)

	if reply, err := rf.HandleRequestVote(&RequestVoteArgs{Term: 5, CandidateID: "B"}); err == nil {
		t.Fatalf("expected no reply while the term is not durable, got %+v", reply)
	}
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("expected candidate to step down, got %v", role)
	}

	st.setFailSave(false)
	reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 5, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected vote granted once storage recovers, got %+v", reply)
	}
	if state, _ := st.LoadState(); state.CurrentTerm != 5 || state.VotedFor != "B" {
		t.Fatalf("vote not persisted: %+v", state)
	}
}

func TestHandleAppendEntries_NonDurableTermGetsNoReplyUntilStorageRecovers(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 1, "A")
	st.setFailSave(true)

	if reply, err := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 2, LeaderID: "B"}); err == nil {
		t.Fatalf("expected no reply while the term is not durable, got %+v", reply)
	}
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("expected leader to step down, got %v", role)
	}
	if !rf.electionTimer.Stop() {
		t.Fatalf("heartbeat from a legitimate leader must re-arm the election timer")
	}

	st.setFailSave(false)
	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected Success=true once storage recovers, got %+v", reply)
	}
	if state, _ := st.LoadState(); state.CurrentTerm != 2 || state.VotedFor != "" {
		t.Fatalf("term not persisted: %+v", state)
	}
}

func TestRestart_AfterNonDurableStepDownKeepsDurableVote(t *testing.T) {
	// The in-memory term was never durable, so a restart rolls it back; the
	// vote cast in the durable term must still hold (Election Safety).
	rf, st := newTestNode(t, "A", "B", "C")
	if !mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"}).VoteGranted {
		t.Fatalf("setup: expected vote for B granted")
	}
	st.setFailSave(true)
	if _, err := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 3, LeaderID: "C"}); err == nil {
		t.Fatalf("setup: expected storage failure")
	}
	st.setFailSave(false)

	restarted := restartTestNode(t, st, "A", "B", "C")

	if term, _ := restarted.GetState(); term != 1 {
		t.Fatalf("expected durable term 1 after restart, got %d", term)
	}
	if reply := mustHandleRequestVote(t, restarted, &RequestVoteArgs{Term: 1, CandidateID: "C"}); reply.VoteGranted {
		t.Fatalf("restarted node voted twice in term 1: %+v", reply)
	}
}
