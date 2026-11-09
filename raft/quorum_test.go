package raft

import (
	"context"
	"testing"
	"time"
)

func succeedingPeer() replyingPeer {
	return replyingPeer{func(args *AppendEntriesArgs) *AppendEntriesReply {
		return &AppendEntriesReply{Term: args.Term, Success: true}
	}}
}

func newTestLeader(t *testing.T, term Term, commit Index, entries ...LogEntry) *Raft {
	t.Helper()
	rf, _ := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entries...)
	setRoleAndTerm(rf, Leader, term, "A")
	rf.mu.Lock()
	rf.commitIndex = commit
	rf.mu.Unlock()
	for _, p := range []NodeID{"B", "C"} {
		rf.transport.(*fakeTransport).register(p, succeedingPeer())
	}
	return rf
}

func replicateTo(rf *Raft, peer NodeID) {
	rf.mu.Lock()
	args := rf.appendEntriesArgsLocked(peer)
	rf.mu.Unlock()
	rf.sendAppendEntries(context.Background(), peer, args)
}

func TestCheckQuorum_StepsDownWithoutQuorum(t *testing.T) {
	rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)

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
	rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
	replicateTo(rf, "B")

	rf.mu.Lock()
	rf.checkQuorumLocked()
	heard := len(rf.heardFrom)
	rf.mu.Unlock()
	if role, term := roleAndTerm(rf); role != Leader || term != 2 {
		t.Fatalf("leader with a quorum became %v in term %d", role, term)
	}
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

func TestCheckQuorum_RejectionInOurTermCounts(t *testing.T) {
	// Конфликт лога не отменяет факт ответа соседа в текущем term.
	rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
	rf.transport.(*fakeTransport).register("B", replyingPeer{func(args *AppendEntriesArgs) *AppendEntriesReply {
		return &AppendEntriesReply{Term: args.Term, ConflictIndex: 1}
	}})
	replicateTo(rf, "B")

	rf.mu.Lock()
	rf.checkQuorumLocked()
	rf.mu.Unlock()
	if role, _ := roleAndTerm(rf); role != Leader {
		t.Fatalf("leader answered by a quorum became %v", role)
	}
}

func TestCheckQuorum_RunsOnLeaderElectionTimeout(t *testing.T) {
	rf := newTestLeader(t, 2, 1, entriesOfTerms(2)...)
	rf.mu.Lock()
	rf.resetElectionTimerLocked()
	rf.mu.Unlock()

	rf.clock.(*fakeClock).Advance(2 * time.Second)
	rf.onElectionTimeout(context.Background())
	if role, term := roleAndTerm(rf); role != Follower || term != 2 {
		t.Fatalf("expected Follower in term 2, got %v in term %d", role, term)
	}
}
