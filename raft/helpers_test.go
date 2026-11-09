package raft

import (
	"context"
	"testing"
	"time"
)

type fatalError string

func panicOnFatal(msg string) { panic(fatalError(msg)) }

func testConfig(st Storage, id NodeID, peers ...NodeID) Config {
	return Config{
		ID:                 id,
		Peers:              peers,
		Transport:          newFakeTransport(),
		Storage:            st,
		ApplyCh:            make(chan ApplyMsg, 64),
		Clock:              newFakeClock(time.Unix(0, 0)),
		ElectionTimeoutMin: time.Second,
		ElectionTimeoutMax: 2 * time.Second,
		electionTimeout:    fixedTimeout(time.Second),
		OnFatal:            panicOnFatal,
	}
}

func newTestNode(t *testing.T, id NodeID, peers ...NodeID) (*Raft, *fakeStorage) {
	t.Helper()
	st := &fakeStorage{}
	return restartTestNode(t, st, id, peers...), st
}

func restartTestNode(t *testing.T, st *fakeStorage, id NodeID, peers ...NodeID) *Raft {
	t.Helper()
	rf, err := New(testConfig(st, id, peers...))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	markRunning(rf)
	return rf
}

func markRunning(rf *Raft) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.started = true
	rf.done = make(chan struct{})
}

func setRoleAndTerm(rf *Raft, role Role, term Term, votedFor NodeID) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.role = role
	rf.currentTerm = term
	rf.votedFor = votedFor
	rf.leaderID = ""
	if role == Leader {
		rf.leaderID = rf.id
		rf.initLeaderStateLocked()
	}
}

func roleAndTerm(rf *Raft) (Role, Term) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.role, rf.currentTerm
}

func votedFor(rf *Raft) NodeID {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.votedFor
}

func mustHandleRequestVote(t *testing.T, rf *Raft, args *RequestVoteArgs) *RequestVoteReply {
	t.Helper()
	reply, err := rf.HandleRequestVote(args)
	if err != nil {
		t.Fatalf("HandleRequestVote(%+v): %v", args, err)
	}
	return reply
}

func mustHandleAppendEntries(t *testing.T, rf *Raft, args *AppendEntriesArgs) *AppendEntriesReply {
	t.Helper()
	reply, err := rf.HandleAppendEntries(args)
	if err != nil {
		t.Fatalf("HandleAppendEntries(%+v): %v", args, err)
	}
	return reply
}

func mustBeFatal(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if _, ok := recover().(fatalError); !ok {
			t.Fatalf("expected OnFatal to be called on a broken invariant")
		}
	}()
	f()
}

func runNode(rf *Raft) (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rf.Run(ctx)
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}
