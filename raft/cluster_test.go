package raft

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

type testCluster struct {
	t        *testing.T
	nodes    []*Raft
	ids      []NodeID
	storages []*fakeStorage
	timeouts []func() time.Duration
	stops    []context.CancelFunc
	done     []chan struct{}
	clock    *fakeClock
	tr       *fakeTransport
}

// Cluster tests run inside a synctest bubble: synctest.Wait tells when every
// node goroutine has finished reacting to a tick, so the tests never sleep and
// time exists only as fakeClock's virtual time.

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
	n := len(timeouts)
	c := &testCluster{
		t:        t,
		nodes:    make([]*Raft, n),
		ids:      make([]NodeID, n),
		storages: make([]*fakeStorage, n),
		timeouts: timeouts,
		stops:    make([]context.CancelFunc, n),
		done:     make([]chan struct{}, n),
		clock:    newFakeClock(time.Unix(0, 0)),
		tr:       newFakeTransport(),
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
		ID:              c.ids[i],
		Peers:           peers,
		Transport:       c.tr,
		Storage:         c.storages[i],
		Clock:           c.clock,
		ElectionTimeout: c.timeouts[i],
	})
	if err != nil {
		c.t.Fatalf("New(%s): %v", c.ids[i], err)
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

func votedFor(rf *Raft) NodeID {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.votedFor
}

// electionLog records the leader seen in each term to check Election Safety
// (§5.2) across the whole run, not only at its end.
type electionLog map[Term]NodeID

func (l electionLog) record(t *testing.T, c *testCluster) {
	t.Helper()
	for _, rf := range c.leaders() {
		term, _ := rf.GetState()
		if prev, ok := l[term]; ok && prev != rf.id {
			t.Fatalf("Election Safety violated: %s and %s are both leaders in term %d", prev, rf.id, term)
		}
		l[term] = rf.id
	}
}

// advanceChecked runs the cluster for d of virtual time, recording leaders
// after every tick.
func (c *testCluster) advanceChecked(t *testing.T, d time.Duration, l electionLog) {
	t.Helper()
	for range d / tick {
		c.step()
		l.record(t, c)
	}
}

// splitFirstElection brings a 5-node cluster to a split vote in term 1: n4 is
// down, n0 holds votes {n0, n2} and n1 holds {n1, n3}, so neither reaches the
// quorum of 3. Partitions are used only to script who reaches whom first;
// on return every node except n4 is connected again.
//
// With newTestCluster timeouts the next deadlines are n0 at 200ms, n1 and n2
// at 220ms, n3 at 240ms.
func splitFirstElection(t *testing.T, c *testCluster, l electionLog) {
	t.Helper()
	c.crash(4)

	c.tr.setPartitioned("n1", true)
	c.tr.setPartitioned("n3", true)
	c.advanceUntil(t, time.Second, func() bool {
		l.record(t, c)
		role, _ := roleAndTerm(c.nodes[0])
		return role == Candidate
	})

	c.tr.setPartitioned("n0", true)
	c.tr.setPartitioned("n2", true)
	c.tr.setPartitioned("n1", false)
	c.tr.setPartitioned("n3", false)
	c.advanceUntil(t, time.Second, func() bool {
		l.record(t, c)
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
		l := electionLog{}
		splitFirstElection(t, c, l)

		c.start(4)

		c.advanceUntil(t, 5*time.Second, func() bool {
			l.record(t, c)
			return len(c.leaders()) == 1
		})
		leader := c.leaders()[0]
		leaderTerm, _ := leader.GetState()
		if leaderTerm < 2 {
			t.Fatalf("split term 1 must not produce a leader, got leader %s in term %d", leader.id, leaderTerm)
		}

		c.advanceChecked(t, 500*time.Millisecond, l)

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
		l := electionLog{}
		splitFirstElection(t, c, l)

		// n4 stays down. Isolating n1 lets n0 (deadline 200ms) collect votes from
		// n2 and n3 while n1 (deadline 220ms) runs for term 2 on its own vote:
		// a 3/1 split among the 4 live nodes, where 3 is still a quorum of 5.
		c.tr.setPartitioned("n1", true)
		c.advanceUntil(t, time.Second, func() bool {
			l.record(t, c)
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
		c.advanceChecked(t, 500*time.Millisecond, l)

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
