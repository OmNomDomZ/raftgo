package raft

import (
	"context"
	"testing"
	"time"
)

// succeedingPeer acknowledges every AppendEntries in the request's term.
func succeedingPeer() replyingPeer {
	return replyingPeer{func(args *AppendEntriesArgs) *AppendEntriesReply {
		return &AppendEntriesReply{Term: args.Term, Success: true}
	}}
}

// newTestLeader creates a leader with two responsive peers.
func newTestLeader(t *testing.T, term Term) *Raft {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, term, "A")
	for _, p := range []NodeID{"B", "C"} {
		rf.transport.(*fakeTransport).register(p, succeedingPeer())
	}
	return rf
}

func heartbeatTo(rf *Raft, peer NodeID) {
	rf.sendAppendEntries(context.Background(), peer, &AppendEntriesArgs{Term: rf.currentTerm, LeaderID: rf.id})
}

func TestCheckQuorum_StepsDownWithoutQuorum(t *testing.T) {
	rf := newTestLeader(t, 2)

	rf.mu.Lock()
	rf.checkQuorumLocked()
	rf.mu.Unlock()
	if role, term := roleAndTerm(rf); role != Follower || term != 2 {
		t.Fatalf("expected Follower in term 2, got %v in term %d", role, term)
	}
	if rf.heartbeatTimer.Stop() {
		t.Fatalf("deposed leader still sends heartbeats")
	}
	if !rf.electionTimer.Stop() {
		t.Fatalf("deposed leader must arm its election timer")
	}
}

func TestCheckQuorum_LeaderWithQuorumStays(t *testing.T) {
	rf := newTestLeader(t, 2)
	heartbeatTo(rf, "B")

	rf.mu.Lock()
	rf.checkQuorumLocked()
	heard := len(rf.heardFrom)
	rf.mu.Unlock()
	if role, term := roleAndTerm(rf); role != Leader || term != 2 {
		t.Fatalf("leader with a quorum became %v in term %d", role, term)
	}
	// Each check needs fresh answers.
	if heard != 0 {
		t.Fatalf("quorum check kept %d answers for the next check", heard)
	}

	rf.mu.Lock()
	rf.checkQuorumLocked()
	rf.mu.Unlock()
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("leader stayed %v with no answers since the last check", role)
	}
}

func TestCheckQuorum_RunsOnLeaderElectionTimeout(t *testing.T) {
	rf := newTestLeader(t, 2)
	rf.mu.Lock()
	rf.resetElectionTimerLocked()
	rf.mu.Unlock()

	rf.clock.(*fakeClock).Advance(2 * time.Second)
	rf.onElectionTimeout(context.Background())
	// No answers, so the leader steps down, but it must not start an
	// election from the same timeout.
	if role, term := roleAndTerm(rf); role != Follower || term != 2 {
		t.Fatalf("expected Follower in term 2, got %v in term %d", role, term)
	}
}
