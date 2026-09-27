package raft

import (
	"context"
	"testing"
)

// These tests cover stepping down while storage is failing: the node must
// adopt the higher term in memory, but must not promise anything based on it
// until it is durable (§5.1).

func setRoleAndTerm(rf *Raft, role Role, term Term, votedFor NodeID) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.role = role
	rf.currentTerm = term
	rf.votedFor = votedFor
}

func roleAndTerm(rf *Raft) (Role, Term) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.role, rf.currentTerm
}

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

	rf.sendHeartbeat(context.Background(), "B", &AppendEntriesArgs{Term: 2, LeaderID: "A"})

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

func TestHandleRequestVote_NonDurableTermDeclinesUntilStorageRecovers(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Candidate, 2, "A")
	st.setFailSave(true)

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 5, CandidateID: "B"})

	if reply.VoteGranted || reply.Term != 5 {
		t.Fatalf("expected vote declined with term 5, got %+v", reply)
	}
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("expected candidate to step down, got %v", role)
	}

	st.setFailSave(false)
	reply = rf.HandleRequestVote(&RequestVoteArgs{Term: 5, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected vote granted once storage recovers, got %+v", reply)
	}
	if state, _ := st.LoadState(); state != (PersistentState{CurrentTerm: 5, VotedFor: "B"}) {
		t.Fatalf("vote not persisted: %+v", state)
	}
}

func TestHandleAppendEntries_NonDurableTermRejectsUntilStorageRecovers(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 1, "A")
	st.setFailSave(true)

	reply := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if reply.Success || reply.Term != 2 {
		t.Fatalf("expected Success=false with term 2, got %+v", reply)
	}
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("expected leader to step down, got %v", role)
	}
	if !rf.electionTimer.Stop() {
		t.Fatalf("heartbeat from a legitimate leader must re-arm the election timer")
	}

	st.setFailSave(false)
	reply = rf.HandleAppendEntries(&AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected Success=true once storage recovers, got %+v", reply)
	}
	if state, _ := st.LoadState(); state != (PersistentState{CurrentTerm: 2}) {
		t.Fatalf("term not persisted: %+v", state)
	}
}

func TestRestart_AfterNonDurableStepDownKeepsDurableVote(t *testing.T) {
	// The in-memory term was never durable, so a restart rolls it back; the
	// vote cast in the durable term must still hold (Election Safety).
	rf, st := newTestNode(t, "A", "B", "C")
	if !rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"}).VoteGranted {
		t.Fatalf("setup: expected vote for B granted")
	}
	st.setFailSave(true)
	rf.HandleAppendEntries(&AppendEntriesArgs{Term: 3, LeaderID: "C"})
	st.setFailSave(false)

	restarted, err := New(testConfig(st, "A", "B", "C"))
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}

	if term, _ := restarted.GetState(); term != 1 {
		t.Fatalf("expected durable term 1 after restart, got %d", term)
	}
	if reply := restarted.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "C"}); reply.VoteGranted {
		t.Fatalf("restarted node voted twice in term 1: %+v", reply)
	}
}
