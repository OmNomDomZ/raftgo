package raft

import (
	"context"
	"testing"
	"time"
)

// fatalError is what panicOnFatal panics with, so tests can tell a reported
// invariant violation from any other panic.
type fatalError string

func panicOnFatal(msg string) { panic(fatalError(msg)) }

func testConfig(st Storage, id NodeID, peers ...NodeID) Config {
	return Config{
		ID:                 id,
		Peers:              peers,
		Transport:          newFakeTransport(),
		Storage:            st,
		Clock:              newFakeClock(time.Unix(0, 0)),
		ElectionTimeout:    fixedTimeout(time.Second),
		ElectionTimeoutMin: time.Second,
		OnFatal:            panicOnFatal,
	}
}

func newTestNode(t *testing.T, id NodeID, peers ...NodeID) (*Raft, *fakeStorage) {
	t.Helper()
	st := &fakeStorage{}
	rf, err := New(testConfig(st, id, peers...))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rf, st
}

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

func votedFor(rf *Raft) NodeID {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.votedFor
}

// mustHandleRequestVote calls rf.HandleRequestVote and fails the test if the
// node could not answer.
func mustHandleRequestVote(t *testing.T, rf *Raft, args *RequestVoteArgs) *RequestVoteReply {
	t.Helper()
	reply, err := rf.HandleRequestVote(args)
	if err != nil {
		t.Fatalf("HandleRequestVote(%+v): %v", args, err)
	}
	return reply
}

// mustHandleAppendEntries calls rf.HandleAppendEntries and fails the test if
// the node could not answer.
func mustHandleAppendEntries(t *testing.T, rf *Raft, args *AppendEntriesArgs) *AppendEntriesReply {
	t.Helper()
	reply, err := rf.HandleAppendEntries(args)
	if err != nil {
		t.Fatalf("HandleAppendEntries(%+v): %v", args, err)
	}
	return reply
}

// mustBeFatal fails the test unless f reports a broken invariant through
// Config.OnFatal.
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

// runNode runs rf in the background; the returned stop cancels Run and
// waits for it to return.
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
