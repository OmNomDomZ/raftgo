package storage_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"raftgo/raft"
	"raftgo/storage"
)

func loadLog(t *testing.T, m *storage.Memory) []raft.LogEntry {
	t.Helper()
	entries, err := m.LoadLog()
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func storeLog(t *testing.T, m *storage.Memory, from raft.Index, entries []raft.LogEntry) {
	t.Helper()
	if err := m.StoreLog(from, entries); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCopiesAndSuffixReplacement(t *testing.T) {
	var m storage.Memory
	if state, err := m.LoadState(); err != nil || state != (raft.PersistentState{}) {
		t.Fatalf("fresh state: %+v, %v", state, err)
	}
	if got := loadLog(t, &m); len(got) != 0 {
		t.Fatalf("fresh log: %+v", got)
	}
	entries := []raft.LogEntry{{Term: 1, Command: []byte("one")}, {Term: 2, Kind: raft.EntryNoOp}, {Term: 2, Command: []byte("old")}}
	storeLog(t, &m, 1, entries)
	entries[0].Command[0] = 'X'
	entries[1].Term = 99
	want := []raft.LogEntry{{Term: 1, Command: []byte("one")}, {Term: 2, Kind: raft.EntryNoOp}, {Term: 2, Command: []byte("old")}}
	got := loadLog(t, &m)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input aliases storage: %+v", got)
	}
	got[0].Command[0] = 'Y'
	got[1].Kind = raft.EntryCommand
	if got = loadLog(t, &m); !reflect.DeepEqual(got, want) {
		t.Fatalf("output aliases storage: %+v", got)
	}
	replacement := []raft.LogEntry{{Term: 3, Command: []byte("new")}}
	storeLog(t, &m, 2, replacement)
	replacement[0].Command[0] = 'Z'
	want = []raft.LogEntry{want[0], {Term: 3, Command: []byte("new")}}
	if got = loadLog(t, &m); !reflect.DeepEqual(got, want) {
		t.Fatalf("suffix replacement: %+v", got)
	}
	storeLog(t, &m, 3, []raft.LogEntry{{Term: 3, Kind: raft.EntryNoOp}})
	if got = loadLog(t, &m); len(got) != 3 || got[2].Kind != raft.EntryNoOp {
		t.Fatalf("append: %+v", got)
	}
	storeLog(t, &m, 3, nil)
	if got = loadLog(t, &m); !reflect.DeepEqual(got, want) {
		t.Fatalf("truncate: %+v", got)
	}
	storeLog(t, &m, 1, nil)
	if got = loadLog(t, &m); len(got) != 0 {
		t.Fatalf("truncate all: %+v", got)
	}
	storeLog(t, &m, 1, nil)
}

func TestMemoryInvalidIndexDoesNotMutate(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			var m storage.Memory
			state := raft.PersistentState{CurrentTerm: 3, VotedFor: "B"}
			if err := m.SaveState(state); err != nil {
				t.Fatal(err)
			}
			if populated {
				storeLog(t, &m, 1, []raft.LogEntry{{Term: 2, Command: []byte("keep")}})
			}
			want := loadLog(t, &m)
			for _, from := range []raft.Index{0, raft.Index(len(want)) + 2, raft.Index(math.MaxUint64)} {
				if err := m.StoreLog(from, []raft.LogEntry{{Term: 3, Command: []byte("bad")}}); err == nil {
					t.Fatalf("accepted index %d", from)
				}
				if got := loadLog(t, &m); !reflect.DeepEqual(got, want) {
					t.Fatalf("index %d changed log: %+v", from, got)
				}
				if got, err := m.LoadState(); err != nil || got != state {
					t.Fatalf("index %d changed state: %+v, %v", from, got, err)
				}
			}
		})
	}
}

func TestMemoryConcurrentOperations(t *testing.T) {
	var m storage.Memory
	var wg sync.WaitGroup
	for worker := 1; worker <= 8; worker++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				term := raft.Term(worker)
				state := raft.PersistentState{CurrentTerm: term, VotedFor: raft.NodeID(fmt.Sprint(term))}
				if err := m.SaveState(state); err != nil {
					t.Error(err)
					return
				}
				if got, err := m.LoadState(); err != nil || got.VotedFor != raft.NodeID(fmt.Sprint(got.CurrentTerm)) {
					t.Errorf("torn state: %+v, %v", got, err)
					return
				}
				entries := []raft.LogEntry{{Term: term, Command: []byte{byte(term)}}, {Term: term, Kind: raft.EntryNoOp}}
				if err := m.StoreLog(1, entries); err != nil {
					t.Error(err)
					return
				}
				entries[0].Command[0] = 255
				got, err := m.LoadLog()
				if err != nil || len(got) != 2 {
					t.Errorf("log: %+v, %v", got, err)
					return
				}
				if got[0].Term != got[1].Term || got[0].Command[0] != byte(got[0].Term) || got[1].Kind != raft.EntryNoOp {
					t.Errorf("torn/aliased log: %+v", got)
					return
				}
				got[0].Command[0] = 254
			}
		})
	}
	wg.Wait()
}

// Тестовые часы отмечают запуск Run, но не запускают выборы по таймеру.
type quietClock struct {
	ready chan struct{}
	once  sync.Once
}

func (c *quietClock) Now() time.Time                    { return time.Unix(0, 0) }
func (c *quietClock) NewTimer(time.Duration) raft.Timer { return quietTimer{c} }

type quietTimer struct{ clock *quietClock }

func (quietTimer) C() <-chan time.Time { return nil }
func (t quietTimer) Reset(time.Duration) bool {
	t.clock.once.Do(func() { close(t.clock.ready) })
	return false
}
func (quietTimer) Stop() bool { return false }

type noTransport struct{}

func (noTransport) RequestVote(context.Context, raft.NodeID, *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	panic("unexpected outgoing vote")
}
func (noTransport) AppendEntries(context.Context, raft.NodeID, *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	panic("unexpected outgoing append")
}

func startMemoryNode(t *testing.T, m *storage.Memory) (*raft.Raft, func()) {
	t.Helper()
	clock := &quietClock{ready: make(chan struct{})}
	node, err := raft.New(raft.Config{ID: "A", Peers: []raft.NodeID{"B", "C"}, Storage: m, Transport: noTransport{}, Clock: clock, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { node.Run(ctx); close(done) }()
	<-clock.ready
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return node, stop
}

func TestMemoryRestartPreservesVoteAndLogMatching(t *testing.T) {
	var m storage.Memory
	node, stop := startMemoryNode(t, &m)
	entries := []raft.LogEntry{{Term: 1, Command: []byte("prefix")}, {Term: 2, Command: []byte("tail")}}
	reply, err := node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: 2, LeaderID: "B", Entries: entries})
	if err != nil || !reply.Success {
		t.Fatalf("initial append: %+v, %v", reply, err)
	}
	vote, err := node.HandleRequestVote(&raft.RequestVoteArgs{Term: 3, CandidateID: "B", LastLogIndex: 2, LastLogTerm: 2})
	if err != nil || !vote.VoteGranted {
		t.Fatalf("first vote: %+v, %v", vote, err)
	}
	stop()
	node, stop = startMemoryNode(t, &m)
	status, got := node.InspectLog()
	if status.Term != 3 || status.VotedFor != "B" || !reflect.DeepEqual(got, entries) {
		t.Fatalf("restart: %+v, %+v", status, got)
	}
	vote, err = node.HandleRequestVote(&raft.RequestVoteArgs{Term: 3, CandidateID: "C", LastLogIndex: 2, LastLogTerm: 2})
	if err != nil || vote.VoteGranted {
		t.Fatalf("second vote in same term: %+v, %v", vote, err)
	}
	reply, err = node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: 3, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 2, Entries: []raft.LogEntry{{Term: 3, Command: []byte("wrong")}}})
	if err != nil || reply.Success {
		t.Fatalf("mismatched prefix accepted: %+v, %v", reply, err)
	}
	if got = loadLog(t, &m); !reflect.DeepEqual(got, entries) {
		t.Fatalf("mismatch changed log: %+v", got)
	}
	replacement := raft.LogEntry{Term: 3, Command: []byte("replacement")}
	reply, err = node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: 3, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: []raft.LogEntry{replacement}})
	if err != nil || !reply.Success {
		t.Fatalf("matching prefix rejected: %+v, %v", reply, err)
	}
	stop()
	node, _ = startMemoryNode(t, &m)
	_, got = node.InspectLog()
	if want := []raft.LogEntry{entries[0], replacement}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restart lost replacement: %+v", got)
	}
	vote, err = node.HandleRequestVote(&raft.RequestVoteArgs{Term: 4, CandidateID: "C", LastLogIndex: 10, LastLogTerm: 2})
	if err != nil || vote.VoteGranted {
		t.Fatalf("stale candidate granted vote: %+v, %v", vote, err)
	}
}
