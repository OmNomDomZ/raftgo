package storage_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"raftgo/raft"
	"raftgo/storage"
)

func TestMemoryConcurrentStateIsAtomic(t *testing.T) {
	var m storage.Memory
	if state, err := m.LoadState(); err != nil || state != (raft.PersistentState{}) {
		t.Fatalf("fresh state: %+v, %v", state, err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			for range 100 {
				st := raft.PersistentState{CurrentTerm: raft.Term(i + 1), VotedFor: raft.NodeID(string(rune('a' + i)))}
				if err := m.SaveState(st); err != nil {
					t.Error(err)
				}
				got, err := m.LoadState()
				if err != nil || got.CurrentTerm == 0 || got.VotedFor != raft.NodeID(string(rune('a'+got.CurrentTerm-1))) {
					t.Errorf("torn state: %+v, %v", got, err)
				}
			}
		})
	}
	wg.Wait()
}

type disconnected struct{}

func (disconnected) RequestVote(ctx context.Context, _ raft.NodeID, _ *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (disconnected) AppendEntries(ctx context.Context, _ raft.NodeID, _ *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRestartWithSameMemoryCannotVoteTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var m storage.Memory
		cfg := raft.Config{ID: "a", Peers: []raft.NodeID{"b", "c"}, Storage: &m, Transport: disconnected{}}
		start := func() (*raft.Raft, func()) {
			node, err := raft.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); node.Run(ctx) }()
			synctest.Wait()
			return node, func() { cancel(); <-done }
		}
		n, stop := start()
		reply, err := n.HandleRequestVote(&raft.RequestVoteArgs{Term: 7, CandidateID: "b"})
		if err != nil || !reply.VoteGranted {
			t.Fatalf("initial vote: %+v, %v", reply, err)
		}
		stop()
		n, stop = start()
		defer stop()
		if s := n.Status(); s.Term != 7 || s.VotedFor != "b" {
			t.Fatalf("lost state: %+v", s)
		}
		for _, candidate := range []raft.NodeID{"c", "b"} {
			reply, err = n.HandleRequestVote(&raft.RequestVoteArgs{Term: 7, CandidateID: candidate})
			if err != nil || reply.VoteGranted != (candidate == "b") {
				t.Fatalf("repeat vote for %s: %+v, %v", candidate, reply, err)
			}
		}
		reply, err = n.HandleRequestVote(&raft.RequestVoteArgs{Term: 6, CandidateID: "c"})
		if err != nil || reply.VoteGranted || reply.Term != 7 {
			t.Fatalf("stale vote: %+v, %v", reply, err)
		}
	})
}
