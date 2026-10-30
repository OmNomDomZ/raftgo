package raft

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type testCluster struct {
	t        *testing.T
	nodes    []*Raft
	ids      []NodeID
	storages []*fakeStorage
	timeouts []func() time.Duration // nil: drawn from rands[i]
	rands    []*rand.Rand
	stops    []context.CancelFunc
	done     []chan struct{}
	clock    *fakeClock
	tr       *fakeTransport
	// elections checks Election Safety on every leader change of every node.
	elections *electionLog
}

// Cluster tests run inside a synctest bubble: synctest.Wait tells when every
// node goroutine has finished reacting to a tick, so the tests never sleep and
// time exists only as fakeClock's virtual time.

// clusterElectionMin and clusterElectionMax bound every election timeout used
// in cluster tests.
const (
	clusterElectionMin = 100 * time.Millisecond
	clusterElectionMax = 400 * time.Millisecond
)

// newTestCluster starts n fully-connected nodes. Node i gets a fixed election
// timeout of 100ms+10ms*i, so n0 always times out first.
func newTestCluster(t *testing.T, n int) *testCluster {
	t.Helper()
	timeouts := make([]func() time.Duration, n)
	for i := range timeouts {
		timeouts[i] = fixedTimeout(time.Duration(100+10*i) * time.Millisecond)
	}
	return newTestClusterWithTimeouts(t, timeouts)
}

// newTestClusterWithTimeouts starts one node per election-timeout source.
func newTestClusterWithTimeouts(t *testing.T, timeouts []func() time.Duration) *testCluster {
	t.Helper()
	return newTestClusterWith(t, timeouts, make([]*rand.Rand, len(timeouts)))
}

// newRandomizedTestCluster starts n nodes that randomize their election
// timeouts through Config.Rand, as in production, seeded from seed.
func newRandomizedTestCluster(t *testing.T, n int, seed uint64) *testCluster {
	t.Helper()
	rands := make([]*rand.Rand, n)
	for i := range rands {
		rands[i] = rand.New(rand.NewPCG(seed, uint64(i)))
	}
	return newTestClusterWith(t, make([]func() time.Duration, n), rands)
}

func newTestClusterWith(t *testing.T, timeouts []func() time.Duration, rands []*rand.Rand) *testCluster {
	t.Helper()
	n := len(timeouts)
	c := &testCluster{
		t:        t,
		nodes:    make([]*Raft, n),
		ids:      make([]NodeID, n),
		storages: make([]*fakeStorage, n),
		timeouts: timeouts,
		rands:    rands,
		stops:    make([]context.CancelFunc, n),
		done:     make([]chan struct{}, n),
		clock:    newFakeClock(time.Unix(0, 0)),
		tr:       newFakeTransport(),
		elections: &electionLog{
			t:       t,
			leaders: map[Term]NodeID{},
		},
	}
	for i := range n {
		c.ids[i] = NodeID(fmt.Sprintf("n%d", i))
		c.storages[i] = &fakeStorage{}
	}
	for i := range n {
		c.start(i)
	}
	t.Cleanup(func() {
		for i := range n {
			c.crash(i)
		}
		c.tr.close()
	})
	return c
}

// start creates node i from its storage, so calling it after crash simulates
// a restart.
func (c *testCluster) start(i int) {
	c.t.Helper()
	var peers []NodeID
	for _, other := range c.ids {
		if other != c.ids[i] {
			peers = append(peers, other)
		}
	}
	rf, err := New(Config{
		ID:                 c.ids[i],
		Peers:              peers,
		Transport:          c.tr,
		Storage:            c.storages[i],
		Clock:              c.clock,
		ElectionTimeoutMin: clusterElectionMin,
		ElectionTimeoutMax: clusterElectionMax,
		Rand:               c.rands[i],
		electionTimeout:    boundedTimeout(c.t, clusterElectionMin, clusterElectionMax, c.timeouts[i]),
		OnFatal:            panicOnFatal,
	})
	if err != nil {
		c.t.Fatalf("New(%s): %v", c.ids[i], err)
	}
	id := c.ids[i]
	rf.onBecomeLeader = func(term Term) {
		c.elections.record(id, term)
	}
	c.tr.register(c.ids[i], rf)
	c.tr.setPartitioned(c.ids[i], false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.nodes[i], c.stops[i], c.done[i] = rf, cancel, done
	go func() {
		defer close(done)
		rf.Run(ctx)
	}()
}

// crash stops node i and cuts it off the network; its storage survives.
func (c *testCluster) crash(i int) {
	if c.stops[i] == nil {
		return
	}
	c.tr.setPartitioned(c.ids[i], true)
	c.stops[i]()
	<-c.done[i]
	c.stops[i] = nil
}

// tick is the virtual-time step; it is well below the heartbeat interval and
// election timeouts, so no timer deadline is skipped over by much.
const tick = 10 * time.Millisecond

// step lets the nodes settle, advances virtual time by one tick and waits
// until every node has reacted to the timers it fired.
func (c *testCluster) step() {
	synctest.Wait()
	c.clock.Advance(tick)
	synctest.Wait()
}

// advance runs the cluster for d of virtual time.
func (c *testCluster) advance(d time.Duration) {
	for range d / tick {
		c.step()
	}
}

// advanceUntil runs the cluster until cond holds, failing if it does not
// within the given amount of virtual time.
func (c *testCluster) advanceUntil(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()
	synctest.Wait()
	for elapsed := time.Duration(0); !cond(); elapsed += tick {
		if elapsed >= within {
			t.Fatalf("condition not met within %s of virtual time", within)
		}
		c.step()
	}
}

func (c *testCluster) leaders() []*Raft {
	var out []*Raft
	for i, rf := range c.nodes {
		if c.stops[i] == nil {
			continue // crashed
		}
		if _, isLeader := rf.GetState(); isLeader {
			out = append(out, rf)
		}
	}
	return out
}

// leaderOtherThan returns a live leader other than old, or nil. A cut-off
// leader steps down on its own (CheckQuorum), so tests that isolate one wait
// for its successor rather than for two leaders at once.
func (c *testCluster) leaderOtherThan(old *Raft) *Raft {
	for _, rf := range c.leaders() {
		if rf != old {
			return rf
		}
	}
	return nil
}

func TestCluster_ElectsExactlyOneLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)

		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })

		leaders := c.leaders()
		if len(leaders) != 1 {
			t.Fatalf("expected exactly one leader, got %d", len(leaders))
		}
		term, _ := leaders[0].GetState()

		// Election Safety (§5.2): at most one leader per term.
		for _, rf := range c.nodes {
			nodeTerm, isLeader := rf.GetState()
			if isLeader && rf != leaders[0] {
				t.Fatalf("more than one leader in term %d", nodeTerm)
			}
			if nodeTerm > term {
				t.Fatalf("node has term %d greater than leader's term %d", nodeTerm, term)
			}
		}
	})
}

func TestCluster_SingleNodeBecomesLeaderImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 1)

		c.advanceUntil(t, 2*time.Second, func() bool { return len(c.leaders()) == 1 })

		if _, isLeader := c.nodes[0].GetState(); !isLeader {
			t.Fatalf("single-node cluster must elect itself leader")
		}
	})
}

func TestCluster_MinorityPartitionCannotElectLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 5)
		c.tr.setPartitioned("n3", true)
		c.tr.setPartitioned("n4", true)

		c.advanceUntil(t, 5*time.Second, func() bool {
			for _, rf := range c.nodes[:3] {
				if _, isLeader := rf.GetState(); isLeader {
					return true
				}
			}
			return false
		})

		// Give the minority plenty of time to run elections of its own.
		c.advance(2500 * time.Millisecond)

		for _, rf := range c.nodes[3:] {
			if _, isLeader := rf.GetState(); isLeader {
				t.Fatalf("node in minority partition must never become leader")
			}
		}

		majorityLeaders := 0
		for _, rf := range c.nodes[:3] {
			if _, isLeader := rf.GetState(); isLeader {
				majorityLeaders++
			}
		}
		if majorityLeaders != 1 {
			t.Fatalf("expected exactly one leader in the majority partition, got %d", majorityLeaders)
		}
	})
}

func TestCluster_HeartbeatsPreventReelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
		leader := c.leaders()[0]
		term, _ := leader.GetState()

		c.advance(800 * time.Millisecond)

		newTerm, isLeader := leader.GetState()
		if !isLeader {
			t.Fatalf("leader lost leadership despite sending heartbeats")
		}
		if newTerm != term {
			t.Fatalf("term changed from %d to %d despite no partition/failure", term, newTerm)
		}
		if len(c.leaders()) != 1 {
			t.Fatalf("expected exactly one leader while original leader is healthy, got %d", len(c.leaders()))
		}
	})
}

func TestCluster_LeaderFailureElectsNewLeaderWithHigherTerm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
		oldLeader := c.leaders()[0]
		oldTerm, _ := oldLeader.GetState()

		c.tr.setPartitioned(oldLeader.id, true)

		c.advanceUntil(t, 5*time.Second, func() bool {
			for _, rf := range c.nodes {
				if rf == oldLeader {
					continue
				}
				if _, isLeader := rf.GetState(); isLeader {
					return true
				}
			}
			return false
		})

		var newLeader *Raft
		for _, rf := range c.nodes {
			if rf == oldLeader {
				continue
			}
			if _, isLeader := rf.GetState(); isLeader {
				newLeader = rf
			}
		}
		if newLeader == nil {
			t.Fatalf("expected a new leader to emerge among the reachable nodes")
		}
		newTerm, _ := newLeader.GetState()
		if newTerm <= oldTerm {
			t.Fatalf("expected new leader's term (%d) to exceed old leader's term (%d)", newTerm, oldTerm)
		}
	})
}

func TestCluster_NewLeaderHeartbeatsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// n0 wins term 1, then draws a long timeout; n1 draws a short one after
		// granting its vote. n0 must heartbeat right away instead of waiting for
		// its own election timer, or n1 deposes a healthy leader.
		c := newTestClusterWithTimeouts(t, []func() time.Duration{
			seqTimeout(150*time.Millisecond, 300*time.Millisecond),
			seqTimeout(200*time.Millisecond, 160*time.Millisecond),
			seqTimeout(200*time.Millisecond, 280*time.Millisecond),
		})
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
		leader := c.leaders()[0]
		term, _ := leader.GetState()

		c.advance(400 * time.Millisecond)

		if newTerm, isLeader := leader.GetState(); !isLeader || newTerm != term {
			t.Fatalf("healthy leader %s was deposed: term %d -> %d, isLeader=%v", leader.id, term, newTerm, isLeader)
		}
	})
}

func TestCluster_RestartedLeaderRejoinsAsFollower(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
		oldIdx := slices.Index(c.nodes, c.leaders()[0])
		oldTerm, _ := c.nodes[oldIdx].GetState()

		c.crash(oldIdx)
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
		newLeader := c.leaders()[0]

		c.start(oldIdx)
		restarted := c.nodes[oldIdx]
		if term, _ := restarted.GetState(); term < oldTerm {
			t.Fatalf("restarted node lost its term: had %d, restored %d", oldTerm, term)
		}

		c.advanceUntil(t, 5*time.Second, func() bool {
			leaderTerm, _ := newLeader.GetState()
			term, isLeader := restarted.GetState()
			return !isLeader && term == leaderTerm
		})
		if leaders := c.leaders(); len(leaders) != 1 || leaders[0] != newLeader {
			t.Fatalf("expected %s to remain the only leader after restart", newLeader.id)
		}
	})
}

// electionLog records every node's elections to check Election Safety
// (§5.2): at most one leader per term over the whole run, including leaders
// that are deposed before a test gets to observe them.
type electionLog struct {
	t       *testing.T
	mu      sync.Mutex
	leaders map[Term]NodeID
}

// record is called from the nodes' goroutines, so it reports a violation
// with Errorf rather than Fatalf.
func (l *electionLog) record(id NodeID, term Term) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.leaders[term]; ok && prev != id {
		l.t.Errorf("Election Safety violated: %s and %s are both leaders in term %d", prev, id, term)
	}
	l.leaders[term] = id
}

// splitFirstElection brings a 5-node cluster to a split vote in term 1: n4 is
// down, n0 holds votes {n0, n2} and n1 holds {n1, n3}, so neither reaches the
// quorum of 3. Partitions are used only to script who reaches whom first;
// on return every node except n4 is connected again.
//
// With newTestCluster timeouts the next deadlines are n0 at 200ms, n1 and n2
// at 220ms, n3 at 240ms.
func splitFirstElection(t *testing.T, c *testCluster) {
	t.Helper()
	c.crash(4)

	c.tr.setPartitioned("n1", true)
	c.tr.setPartitioned("n3", true)
	c.advanceUntil(t, time.Second, func() bool {
		role, _ := roleAndTerm(c.nodes[0])
		return role == Candidate
	})

	c.tr.setPartitioned("n0", true)
	c.tr.setPartitioned("n2", true)
	c.tr.setPartitioned("n1", false)
	c.tr.setPartitioned("n3", false)
	c.advanceUntil(t, time.Second, func() bool {
		role, _ := roleAndTerm(c.nodes[1])
		return role == Candidate
	})

	for i, want := range []NodeID{"n0", "n1", "n0", "n1"} {
		rf := c.nodes[i]
		if _, term := roleAndTerm(rf); term != 1 {
			t.Fatalf("%s: expected term 1, got %d", rf.id, term)
		}
		if got := votedFor(rf); got != want {
			t.Fatalf("%s: expected vote for %s in term 1, got %q", rf.id, want, got)
		}
	}
	for _, rf := range c.nodes[:2] {
		if role, _ := roleAndTerm(rf); role != Candidate {
			t.Fatalf("%s: expected to stay Candidate after a 2/2 split, got %v", rf.id, role)
		}
	}

	c.tr.setPartitioned("n0", false)
	c.tr.setPartitioned("n2", false)
}

func TestCluster_SplitVoteThenCrashedNodeRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 5)
		splitFirstElection(t, c)

		c.start(4)

		c.advanceUntil(t, 5*time.Second, func() bool {
			return len(c.leaders()) == 1
		})
		leader := c.leaders()[0]
		leaderTerm, _ := leader.GetState()
		if leaderTerm < 2 {
			t.Fatalf("split term 1 must not produce a leader, got leader %s in term %d", leader.id, leaderTerm)
		}

		c.advance(500 * time.Millisecond)

		if term, isLeader := leader.GetState(); !isLeader || term != leaderTerm {
			t.Fatalf("leader %s was deposed: term %d -> %d, isLeader=%v", leader.id, leaderTerm, term, isLeader)
		}
		for _, rf := range c.nodes {
			if rf == leader {
				continue
			}
			if role, term := roleAndTerm(rf); role != Follower || term != leaderTerm {
				t.Fatalf("%s: expected Follower in term %d, got %v in term %d", rf.id, leaderTerm, role, term)
			}
		}
	})
}

func TestCluster_SplitVoteThenThreeToOneElectsLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 5)
		splitFirstElection(t, c)

		// n4 stays down. Isolating n1 lets n0 (deadline 200ms) collect votes from
		// n2 and n3 while n1 (deadline 220ms) runs for term 2 on its own vote:
		// a 3/1 split among the 4 live nodes, where 3 is still a quorum of 5.
		c.tr.setPartitioned("n1", true)
		c.advanceUntil(t, time.Second, func() bool {
			role, term := roleAndTerm(c.nodes[1])
			return role == Candidate && term == 2
		})

		if term, isLeader := c.nodes[0].GetState(); !isLeader || term != 2 {
			t.Fatalf("n0: expected Leader in term 2, got term %d isLeader=%v", term, isLeader)
		}
		for i, want := range []NodeID{"n0", "n1", "n0", "n0"} {
			rf := c.nodes[i]
			if _, term := roleAndTerm(rf); term != 2 {
				t.Fatalf("%s: expected term 2, got %d", rf.id, term)
			}
			if got := votedFor(rf); got != want {
				t.Fatalf("%s: expected vote for %s in term 2, got %q", rf.id, want, got)
			}
		}

		// The losing candidate must accept the winner's heartbeats instead of
		// deposing it.
		c.tr.setPartitioned("n1", false)
		c.advance(500 * time.Millisecond)

		if term, isLeader := c.nodes[0].GetState(); !isLeader || term != 2 {
			t.Fatalf("n0 was deposed: term %d isLeader=%v", term, isLeader)
		}
		for _, rf := range c.nodes[1:4] {
			if role, term := roleAndTerm(rf); role != Follower || term != 2 {
				t.Fatalf("%s: expected Follower in term 2, got %v in term %d", rf.id, role, term)
			}
		}
	})
}

func TestCluster_CutOffLeaderStepsDownAndRejoinsAsFollower(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		oldLeader := c.waitLeader(t)
		oldTerm, _ := oldLeader.GetState()

		// Cut off, the old leader hears from no quorum and steps down within
		// two election timeouts: the first check may still count answers
		// from before the partition (CheckQuorum).
		c.tr.setPartitioned(oldLeader.id, true)
		c.advanceUntil(t, 2*clusterElectionMax, func() bool {
			_, isLeader := oldLeader.GetState()
			return !isLeader
		})
		c.advanceUntil(t, 5*time.Second, func() bool {
			newLeader := c.leaderOtherThan(oldLeader)
			if newLeader == nil {
				return false
			}
			term, _ := newLeader.GetState()
			return term > oldTerm
		})

		// In isolation the old leader keeps starting elections, so its term
		// may be ahead and force one more election once it rejoins. Any node can
		// win the next term; all others must follow its heartbeats.
		c.tr.setPartitioned(oldLeader.id, false)
		c.advanceUntil(t, 5*time.Second, func() bool {
			leaders := c.leaders()
			if len(leaders) != 1 {
				return false
			}
			leaderTerm, _ := leaders[0].GetState()
			return c.allFollow(leaders[0], nil) && leaderTerm > oldTerm
		})
	})
}

func TestCluster_EveryNodeKnowsTheLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		old := c.waitLeader(t)
		c.advanceUntil(t, clusterElectionMin, func() bool { return c.allFollow(old, nil) })

		// Cut off, old steps down and no longer knows a leader, while the
		// majority follows the one it elects.
		c.tr.setPartitioned(old.id, true)
		var leader *Raft
		c.advanceUntil(t, 5*time.Second, func() bool {
			leader = c.leaderOtherThan(old)
			return leader != nil && old.Leader() == "" && c.allFollow(leader, old)
		})

		c.tr.setPartitioned(old.id, false)
		c.advanceUntil(t, 5*time.Second, func() bool {
			leaders := c.leaders()
			return len(leaders) == 1 && c.allFollow(leaders[0], nil)
		})
	})
}

// allFollow reports whether every live node other than except names leader
// as its leader.
func (c *testCluster) allFollow(leader, except *Raft) bool {
	for i, rf := range c.nodes {
		if c.stops[i] != nil && rf != except && rf.Leader() != leader.id {
			return false
		}
	}
	return true
}

func TestCluster_HealthyLeaderPassesQuorumChecks(t *testing.T) {
	// One peer down still leaves a quorum, so the leader must keep its term
	// through many quorum checks.
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader := c.waitLeader(t)
		term, _ := leader.GetState()
		c.crash(c.followerOf(leader))

		c.advance(10 * clusterElectionMax)
		if newTerm, isLeader := leader.GetState(); !isLeader || newTerm != term {
			t.Fatalf("leader with a quorum lost its term: term %d -> %d, isLeader=%v", term, newTerm, isLeader)
		}
	})
}

// TestCluster_RandomFaultsKeepSafety checks every leader transition during
// message loss, partitions and restarts with the same safe storage.
func TestCluster_RandomFaultsKeepSafety(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				runRandomFaults(t, seed, false)
			})
		})
	}
}

// TestCluster_RandomDelaysKeepSafety adds random latency to the faults of
// TestCluster_RandomFaultsKeepSafety. Some messages outlive their sender's
// timeout, so requests arrive late and out of order: an AppendEntries after
// a newer one of the same leader, or at a node that has restarted since.
func TestCluster_RandomDelaysKeepSafety(t *testing.T) {
	late := 0
	// More seeds than TestCluster_RandomFaultsKeepSafety: a reordering bug
	// needs an unlucky late message, which only some runs produce.
	for seed := range uint64(100) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				late += runRandomFaults(t, seed, true)
			})
		})
	}
	// Per seed it depends on how much traffic the faults let through.
	if late == 0 && !t.Failed() {
		t.Fatalf("setup: no request arrived after its sender gave up on it")
	}
}

// runRandomFaults returns how many requests arrived after their sender had
// given up on them.
func runRandomFaults(t *testing.T, seed uint64, delays bool) (late int) {
	const n = 5
	c := newRandomizedTestCluster(t, n, seed)

	dropRNG := rand.New(rand.NewPCG(seed, 1000)) // called under the transport's mu
	c.tr.setDrop(func() bool { return dropRNG.Float64() < 0.1 })
	if delays {
		// Mostly well within AppendEntriesTimeout, sometimes far beyond it.
		delayRNG := rand.New(rand.NewPCG(seed, 3000)) // called under the transport's mu
		c.tr.setDelay(c.clock, func() time.Duration {
			if delayRNG.Float64() < 0.2 {
				return 60*time.Millisecond + time.Duration(delayRNG.Int64N(int64(240*time.Millisecond)))
			}
			return time.Duration(delayRNG.Int64N(int64(20 * time.Millisecond)))
		})
	}

	faults := rand.New(rand.NewPCG(seed, 2000))
	partitioned := make([]bool, n)
	for range 3 * time.Second / tick {
		if faults.Float64() < 0.05 {
			i := faults.IntN(n)
			switch {
			case c.stops[i] == nil:
				c.start(i)
				partitioned[i] = false
			case faults.Float64() < 0.3:
				c.crash(i)
			default:
				partitioned[i] = !partitioned[i]
				c.tr.setPartitioned(c.ids[i], partitioned[i])
			}
		}
		c.step()
	}

	late = c.tr.lateCount()
	c.tr.setDrop(nil)
	c.tr.setDelay(nil, nil)
	for i := range n {
		if c.stops[i] == nil {
			c.start(i)
		}
		c.tr.setPartitioned(c.ids[i], false)
	}
	// A follower that missed heartbeats before the heal may still time out
	// and legitimately depose the first leader. A leader that has held on for
	// a full election timeout has reset every follower's timer.
	var leader *Raft
	var leaderTerm Term
	c.advanceUntil(t, 5*time.Second, func() bool {
		leaders := c.leaders()
		if len(leaders) != 1 {
			return false
		}
		term, _ := leaders[0].GetState()
		if leaders[0] != leader || term != leaderTerm {
			leader, leaderTerm = leaders[0], term
			return false
		}
		c.advance(clusterElectionMax)
		term, isLeader := leader.GetState()
		return isLeader && term == leaderTerm
	})
	c.advance(time.Second)
	if term, isLeader := leader.GetState(); !isLeader || term != leaderTerm {
		t.Fatalf("leader %s of a healed cluster was deposed: term %d -> %d", leader.id, leaderTerm, term)
	}
	return late
}

func (c *testCluster) waitLeader(t *testing.T) *Raft {
	t.Helper()
	c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
	return c.leaders()[0]
}

func (c *testCluster) followerOf(leader *Raft) int {
	for i, rf := range c.nodes {
		if rf != leader && c.stops[i] != nil {
			return i
		}
	}
	c.t.Fatal("no live follower")
	return -1
}
