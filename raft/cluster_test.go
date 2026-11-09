package raft

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type testCluster struct {
	t          *testing.T
	nodes      []*Raft
	ids        []NodeID
	storages   []*fakeStorage
	timeouts   []func() time.Duration
	rands      []*rand.Rand
	stops      []context.CancelFunc
	done       []chan struct{}
	clock      *fakeClock
	tr         *fakeTransport
	elections  *electionLog
	applied    *applyLog
	leaderLogs map[NodeID]leaderLog
}

// Виртуальное время продвигается после завершения реакции всех горутин.

const (
	clusterElectionMin = 100 * time.Millisecond
	clusterElectionMax = 400 * time.Millisecond
)

func newTestCluster(t *testing.T, n int) *testCluster {
	t.Helper()
	timeouts := make([]func() time.Duration, n)
	for i := range timeouts {
		timeouts[i] = fixedTimeout(time.Duration(100+10*i) * time.Millisecond)
	}
	return newTestClusterWithTimeouts(t, timeouts)
}

func newTestClusterWithTimeouts(t *testing.T, timeouts []func() time.Duration) *testCluster {
	t.Helper()
	return newTestClusterWith(t, timeouts, make([]*rand.Rand, len(timeouts)))
}

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
		applied:    newApplyLog(t, n),
		leaderLogs: map[NodeID]leaderLog{},
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

// Повторный запуск использует прежнее хранилище узла.
func (c *testCluster) start(i int) {
	c.t.Helper()
	var peers []NodeID
	for _, other := range c.ids {
		if other != c.ids[i] {
			peers = append(peers, other)
		}
	}
	applyCh := make(chan ApplyMsg)
	rf, err := New(Config{
		ID:                 c.ids[i],
		Peers:              peers,
		Transport:          c.tr,
		Storage:            c.storages[i],
		ApplyCh:            applyCh,
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
		c.applied.checkLeaderCompleteness(id, term, rf.log)
	}
	c.tr.register(c.ids[i], rf)
	c.tr.setPartitioned(c.ids[i], false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.nodes[i], c.stops[i], c.done[i] = rf, cancel, done
	gen := c.applied.restart(i)
	go func() {
		defer close(done)
		rf.Run(ctx)
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-applyCh:
				c.applied.record(i, gen, msg)
			}
		}
	}()
}

// Останавливаем узел и связь, сохраняя его Storage.
func (c *testCluster) crash(i int) {
	if c.stops[i] == nil {
		return
	}
	c.tr.setPartitioned(c.ids[i], true)
	c.stops[i]()
	<-c.done[i]
	c.stops[i] = nil
}

const tick = 10 * time.Millisecond

func (c *testCluster) step() {
	synctest.Wait()
	c.clock.Advance(tick)
	synctest.Wait()
	c.checkLogs()
}

func (c *testCluster) advance(d time.Duration) {
	for range d / tick {
		c.step()
	}
}

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
			continue
		}
		if _, isLeader := rf.GetState(); isLeader {
			out = append(out, rf)
		}
	}
	return out
}

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

type electionLog struct {
	t       *testing.T
	mu      sync.Mutex
	leaders map[Term]NodeID
}

func (l *electionLog) record(id NodeID, term Term) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.leaders[term]; ok && prev != id {
		l.t.Errorf("Election Safety violated: %s and %s are both leaders in term %d", prev, id, term)
	}
	l.leaders[term] = id
}

// Создаём ничью: два кандидата получают по два голоса из пяти.
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

		// Первая проверка может учесть старые ответы, поэтому ждём два окна кворума.
		c.tr.setPartitioned(oldLeader.id, true)
		c.advanceUntil(t, 2*clusterElectionMax, func() bool {
			_, isLeader := oldLeader.GetState()
			return !isLeader
		})
		if _, _, err := oldLeader.Start([]byte("x")); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("cut-off leader still accepts commands: %v", err)
		}
		c.advanceUntil(t, 5*time.Second, func() bool {
			newLeader := c.leaderOtherThan(oldLeader)
			if newLeader == nil {
				return false
			}
			term, _ := newLeader.GetState()
			return term > oldTerm
		})

		// После изоляции term узла может вырасти, но устаревший лог не должен победить.
		c.tr.setPartitioned(oldLeader.id, false)
		c.advanceUntil(t, 5*time.Second, func() bool {
			leaders := c.leaders()
			if len(leaders) != 1 || leaders[0] == oldLeader {
				return false
			}
			leaderTerm, _ := leaders[0].GetState()
			role, term := roleAndTerm(oldLeader)
			return role == Follower && term == leaderTerm
		})
	})
}

func TestCluster_EveryNodeKnowsTheLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		old := c.waitLeader(t)
		c.advanceUntil(t, clusterElectionMin, func() bool { return c.allFollow(old, nil) })

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

func (c *testCluster) allFollow(leader, except *Raft) bool {
	for i, rf := range c.nodes {
		if c.stops[i] != nil && rf != except && rf.Leader() != leader.id {
			return false
		}
	}
	return true
}

func TestCluster_HealthyLeaderPassesQuorumChecks(t *testing.T) {
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

// Проверяем безопасность при случайных потерях сообщений, разделениях и перезапусках.
func TestCluster_RandomFaultsKeepSafety(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				runRandomFaults(t, seed, false)
			})
		})
	}
}

// Добавляем задержки, чтобы старые запросы приходили после смены term и роли.
func TestCluster_RandomDelaysKeepSafety(t *testing.T) {
	late := 0
	for seed := range uint64(100) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				late += runRandomFaults(t, seed, true)
			})
		})
	}
	if late == 0 && !t.Failed() {
		t.Fatalf("setup: no request arrived after its sender gave up on it")
	}
}

func runRandomFaults(t *testing.T, seed uint64, delays bool) (late int) {
	const n = 5
	c := newRandomizedTestCluster(t, n, seed)

	dropRNG := rand.New(rand.NewPCG(seed, 1000))
	c.tr.setDrop(func() bool { return dropRNG.Float64() < 0.1 })
	if delays {
		delayRNG := rand.New(rand.NewPCG(seed, 3000))
		c.tr.setDelay(c.clock, func() time.Duration {
			if delayRNG.Float64() < 0.2 {
				return 60*time.Millisecond + time.Duration(delayRNG.Int64N(int64(240*time.Millisecond)))
			}
			return time.Duration(delayRNG.Int64N(int64(20 * time.Millisecond)))
		})
	}

	faults := rand.New(rand.NewPCG(seed, 2000))
	partitioned := make([]bool, n)
	for step := range 3 * time.Second / tick {
		if faults.Float64() < 0.3 {
			for _, rf := range c.leaders() {
				_, _, _ = rf.Start([]byte(fmt.Sprint("cmd", step)))
			}
		}
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
	// Ждём устойчивого лидера: вернувшийся follower ещё может запустить выборы.
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
	c.one(t, "final", n)
	return late
}
