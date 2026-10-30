package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/raft"
	"raftgo/storage"
)

// applyTestTransport lets a leader be elected, then isolates it without
// discarding its term, vote or log. No network or wall-clock sleeps are used.
type applyTestTransport struct{ isolated atomic.Bool }

func (tr *applyTestTransport) RequestVote(_ context.Context, _ raft.NodeID, a *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	if tr.isolated.Load() {
		return nil, errors.New("isolated")
	}
	return &raft.RequestVoteReply{Term: a.Term, VoteGranted: true}, nil
}

func (tr *applyTestTransport) AppendEntries(_ context.Context, _ raft.NodeID, a *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	if tr.isolated.Load() {
		return nil, errors.New("isolated")
	}
	return &raft.AppendEntriesReply{Term: a.Term, Success: true}, nil
}

func TestStringWriteWaitsForOwnAppliedPair(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "commit_waits_for_consumer"
		if replace {
			name = "same_index_other_term_is_conflict"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tr := &applyTestTransport{}
				ch := make(chan raft.ApplyMsg, 16)
				node, err := raft.New(raft.Config{
					ID: "a", Peers: []raft.NodeID{"b", "c"}, Storage: &storage.Memory{},
					Transport: tr, ApplyCh: ch, ElectionTimeoutMin: 100 * time.Millisecond,
					ElectionTimeoutMax: 200 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go node.Run(ctx)
				for i := 0; node.Status().Role != "Leader"; i++ {
					if i == 30 {
						t.Fatal("no leader")
					}
					time.Sleep(10 * time.Millisecond)
				}
				synctest.Wait()
				if replace {
					tr.isolated.Store(true)
				}
				api := NewAPI(node, nil)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				go func() {
					defer close(done)
					api.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/entries", strings.NewReader(`{"command":"строка"}`)))
				}()
				synctest.Wait()
				s, log := node.InspectLog()
				index := raft.Index(len(log))
				if index != 2 || string(log[index-1].Command) != "строка" {
					t.Fatalf("proposal missing: %+v", log)
				}
				select {
				case <-done:
					t.Fatal("write returned before application")
				default:
				}
				if replace {
					if s.CommitIndex >= index {
						t.Fatal("isolated leader committed its proposal")
					}
					_, err := node.HandleAppendEntries(&raft.AppendEntriesArgs{
						Term: s.Term + 1, LeaderID: "b", PrevLogIndex: 1, PrevLogTerm: log[0].Term,
						Entries: []raft.LogEntry{{Term: s.Term + 1, Kind: raft.EntryNoOp}}, LeaderCommit: index,
					})
					if err != nil {
						t.Fatal(err)
					}
				} else if s.CommitIndex != index {
					t.Fatal("setup: proposal not committed")
				}
				go api.Consume(ctx, ch)
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("write did not resolve after application")
				}
				want := 201
				if replace {
					want = 409
				}
				if w.Code != want {
					t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
				}
			})
		})
	}
}
