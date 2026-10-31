package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/kv"
	"raftgo/raft"
	"raftgo/storage"
)

// Actual Raft peers, without HTTP or wall-clock waits. synctest virtualizes
// SystemClock timers. Isolating a node preserves all its in-memory state.
type atomicNetwork struct {
	mu       sync.Mutex
	nodes    map[raft.NodeID]*raft.Raft
	isolated raft.NodeID
}

func (n *atomicNetwork) route(from, to raft.NodeID) (*raft.Raft, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.isolated == from || n.isolated == to {
		return nil, errors.New("isolated")
	}
	return n.nodes[to], nil
}
func (n *atomicNetwork) RequestVote(_ context.Context, to raft.NodeID, a *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	p, err := n.route(a.CandidateID, to)
	if err != nil {
		return nil, err
	}
	return p.HandleRequestVote(a)
}
func (n *atomicNetwork) AppendEntries(_ context.Context, to raft.NodeID, a *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	p, err := n.route(a.LeaderID, to)
	if err != nil {
		return nil, err
	}
	return p.HandleAppendEntries(a)
}

func atomicUntil(t *testing.T, condition func() bool) {
	t.Helper()
	synctest.Wait()
	for i := 0; !condition(); i++ {
		if i == 1000 {
			t.Fatal("condition not met within 10s virtual time")
		}
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
	}
}

func TestKVAtomicCommandsReplicateFailoverAndReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		net := &atomicNetwork{nodes: make(map[raft.NodeID]*raft.Raft)}
		ids := []raft.NodeID{"a", "b", "c"}
		apis := make(map[raft.NodeID]*API)
		memories := make(map[raft.NodeID]*storage.Memory)
		stops := make(map[raft.NodeID]context.CancelFunc)
		dones := make(map[raft.NodeID]chan struct{})
		start := func(id raft.NodeID) {
			var peers []raft.NodeID
			for _, other := range ids {
				if other != id {
					peers = append(peers, other)
				}
			}
			ch := make(chan raft.ApplyMsg, 64)
			node, err := raft.New(raft.Config{ID: id, Peers: peers, Storage: memories[id], Transport: net, ApplyCh: ch,
				ElectionTimeoutMin: 150 * time.Millisecond, ElectionTimeoutMax: 300 * time.Millisecond,
				HeartbeatInterval: 30 * time.Millisecond, Rand: rand.New(rand.NewPCG(uint64(id[0]), 1))})
			if err != nil {
				t.Fatal(err)
			}
			api := NewAPI(node, nil)
			apis[id] = api
			net.mu.Lock()
			net.nodes[id] = node
			net.mu.Unlock()
			nodeCtx, stop := context.WithCancel(ctx)
			stops[id], dones[id] = stop, make(chan struct{})
			done := dones[id]
			go func() { defer close(done); node.Run(nodeCtx) }()
			go api.Consume(nodeCtx, ch)
		}
		for _, id := range ids {
			memories[id] = &storage.Memory{}
			start(id)
		}
		leader := func(except raft.NodeID) *API {
			for _, id := range ids {
				if id != except && apis[id].Node.Status().Role == "Leader" {
					return apis[id]
				}
			}
			return nil
		}
		atomicUntil(t, func() bool { return leader("") != nil })
		a := leader("")
		// Propose directly through Raft, exercising the core and actual consumer.
		propose := func(api *API, c kv.Command) raft.Index {
			t.Helper()
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			index, _, err := api.Node.Start(append([]byte(kv.Prefix), data...))
			if err != nil {
				t.Fatal(err)
			}
			return index
		}
		result := func(api *API, index raft.Index) kv.Result {
			t.Helper()
			atomicUntil(t, func() bool { api.mu.Lock(); defer api.mu.Unlock(); return api.applied >= index })
			api.mu.Lock()
			defer api.mu.Unlock()
			return api.results[index].result
		}
		apply := func(api *API, c kv.Command, code int) kv.Result {
			t.Helper()
			r := result(api, propose(api, c))
			if r.Code != code {
				t.Fatalf("%+v: %+v, want %d", c, r, code)
			}
			return r
		}
		race := func(commands [2]kv.Command, codes [2]int) []kv.Result {
			t.Helper()
			indices := make(chan raft.Index, 2)
			before := a.Node.Status().LastLogIndex
			for _, c := range commands {
				go func() {
					data, _ := json.Marshal(c)
					i, _, err := a.Node.Start(append([]byte(kv.Prefix), data...))
					if err != nil {
						t.Error(err)
					}
					indices <- i
				}()
			}
			x, y := <-indices, <-indices
			if x == y || a.Node.Status().LastLogIndex != before+2 {
				t.Fatal("each operation must be exactly one log entry")
			}
			rs := []kv.Result{result(a, x), result(a, y)}
			counts := map[int]int{}
			for _, r := range rs {
				counts[r.Code]++
			}
			if counts[codes[0]] != 1 || counts[codes[1]] != 1 {
				t.Fatalf("race results: %+v", rs)
			}
			return rs
		}
		apply(a, kv.Command{Op: "put", Key: "x", Value: "a"}, 200)
		cas := race([2]kv.Command{{Op: "cas", Key: "x", Expected: "a", Value: "b"}, {Op: "cas", Key: "x", Expected: "a", Value: "c"}}, [2]int{200, 409})
		if cas[0].Value != cas[1].Value || (cas[0].Value != "b" && cas[0].Value != "c") {
			t.Fatalf("CAS results do not report winning value: %+v", cas)
		}
		rs := race([2]kv.Command{{Op: "lock", Key: "absent"}, {Op: "lock", Key: "absent"}}, [2]int{201, 423})
		var token string
		for _, r := range rs {
			if r.Code == 201 {
				token = r.Token
				if token != strconv.FormatUint(r.Index, 10) {
					t.Fatal("fence is not committed index")
				}
			}
		}
		oldID := a.Node.Status().ID
		oldTerm := a.Node.Status().Term
		net.mu.Lock()
		net.isolated = oldID
		net.mu.Unlock()
		// An isolated leader may append but cannot apply or issue a fence.
		tail := propose(a, kv.Command{Op: "lock", Key: "uncommitted"})
		atomicUntil(t, func() bool { return leader(oldID) != nil })
		b := leader(oldID)
		if b.Node.Status().Term <= oldTerm {
			t.Fatal("new leader must have higher term")
		}
		apply(b, kv.Command{Op: "lock", Key: "absent"}, 423)
		apply(b, kv.Command{Op: "create", Key: "absent", Token: token, Value: "v"}, 201)
		apply(b, kv.Command{Op: "cas", Key: "absent", Token: token, Expected: "v", Value: "w"}, 200)
		apply(b, kv.Command{Op: "delete", Key: "absent", Token: token}, 200)
		apply(b, kv.Command{Op: "create", Key: "absent", Value: "intruder"}, 423)
		apply(b, kv.Command{Op: "create", Key: "absent", Token: token, Value: "reborn"}, 201)
		apply(b, kv.Command{Op: "unlock", Key: "absent", Token: token}, 200)
		apply(b, kv.Command{Op: "put", Key: "absent", Token: token}, 409)
		next := apply(b, kv.Command{Op: "lock", Key: "absent"}, 201)
		previous, _ := strconv.ParseUint(token, 10, 64)
		if next.Index <= previous || next.Token != strconv.FormatUint(next.Index, 10) {
			t.Fatal("fence did not increase after failover")
		}
		apply(b, kv.Command{Op: "unlock", Key: "absent", Token: token}, 423)
		a.mu.Lock()
		if a.applied >= tail || a.store.Get("uncommitted", "").Code != 404 {
			t.Error("isolated proposal was applied")
		}
		a.mu.Unlock()
		// Replay into a new consumer with the SAME Memory; keep other peers live.
		stops[oldID]()
		<-dones[oldID]
		synctest.Wait()
		start(oldID)
		a = apis[oldID]
		a.mu.Lock()
		if a.applied != 0 || len(a.results) != 0 {
			t.Error("replayed without leader commit evidence")
		}
		a.mu.Unlock()
		net.mu.Lock()
		net.isolated = ""
		net.mu.Unlock()
		last := b.Node.Status().LastLogIndex
		for _, id := range ids {
			result(apis[id], last)
		}
		_, expectedLog := b.Node.InspectLog()
		for _, id := range ids {
			api := apis[id]
			_, log := api.Node.InspectLog()
			if !reflect.DeepEqual(log, expectedLog) {
				t.Fatalf("%s: log mismatch", id)
			}
			api.mu.Lock()
			if !reflect.DeepEqual(api.results, b.results) || api.store.Get("absent", next.Token).Value != "reborn" || api.store.Get("uncommitted", "").Code != 404 || api.store.Get("x", "").Value != cas[0].Value {
				t.Errorf("%s: replay/results/lock diverged", id)
			}
			api.mu.Unlock()
		}
	})
}
