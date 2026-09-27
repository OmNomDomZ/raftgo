package raft

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// applyLog records what every node sends on ApplyCh. Anything applied by
// some node is committed, which is what the checks below rely on.
type applyLog struct {
	t  *testing.T
	mu sync.Mutex
	// committed is every command applied by any node, by index.
	committed map[Index][]byte
	// gens counts each node's incarnations, so a message a crashed
	// incarnation sent is not attributed to the restarted one.
	gens []int
	// nodes and last are per node, for its current incarnation.
	nodes []map[Index][]byte
	last  []Index
}

func newApplyLog(t *testing.T, n int) *applyLog {
	return &applyLog{
		t:         t,
		committed: map[Index][]byte{},
		gens:      make([]int, n),
		nodes:     make([]map[Index][]byte, n),
		last:      make([]Index, n),
	}
}

// restart starts a new incarnation of node i, which applies from scratch.
func (l *applyLog) restart(i int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gens[i]++
	l.nodes[i] = map[Index][]byte{}
	l.last[i] = 0
	return l.gens[i]
}

// record is called from the collector goroutines, so it reports violations
// with Errorf rather than Fatalf.
func (l *applyLog) record(i, gen int, msg ApplyMsg) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen != l.gens[i] {
		return
	}
	if msg.Index <= l.last[i] {
		l.t.Errorf("node %d applied index %d after index %d", i, msg.Index, l.last[i])
	}
	l.last[i] = msg.Index
	if prev, ok := l.committed[msg.Index]; ok && !bytes.Equal(prev, msg.Command) {
		l.t.Errorf("State Machine Safety violated: index %d applied as %q and as %q", msg.Index, prev, msg.Command)
	}
	l.committed[msg.Index] = msg.Command
	l.nodes[i][msg.Index] = msg.Command
}

// checkLeaderCompleteness checks that a new leader's log holds every entry
// committed so far (§5.4).
func (l *applyLog) checkLeaderCompleteness(id NodeID, term Term, log []LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, cmd := range l.committed {
		if int(i) >= len(log) || log[i].Kind != EntryCommand || !bytes.Equal(log[i].Command, cmd) {
			l.t.Errorf("Leader Completeness violated: leader %s of term %d lacks committed entry %d %q",
				id, term, i, cmd)
		}
	}
}

// count returns how many nodes have applied cmd at index in their current
// incarnation.
func (l *applyLog) count(index Index, cmd string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, applied := range l.nodes {
		if got, ok := applied[index]; ok && string(got) == cmd {
			n++
		}
	}
	return n
}

// appliedCommands returns what node i has applied in its current
// incarnation, in index order.
func (l *applyLog) appliedCommands(i int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var idx []Index
	for k := range l.nodes[i] {
		idx = append(idx, k)
	}
	slices.Sort(idx)
	var cmds []string
	for _, k := range idx {
		cmds = append(cmds, string(l.nodes[i][k]))
	}
	return cmds
}

type leaderLog struct {
	term Term
	log  []LogEntry
}

// checkLogs checks Log Matching between every pair of nodes and Leader
// Append-Only for every leader (§5.3, Figure 3). step calls it after every
// tick, so every cluster test checks both.
func (c *testCluster) checkLogs() {
	c.t.Helper()
	type snapshot struct {
		id   NodeID
		role Role
		term Term
		log  []LogEntry
	}
	snaps := make([]snapshot, len(c.nodes))
	for i, rf := range c.nodes {
		rf.mu.Lock()
		snaps[i] = snapshot{rf.id, rf.role, rf.currentTerm, slices.Clone(rf.log)}
		rf.mu.Unlock()
	}

	for i, a := range snaps {
		for _, b := range snaps[i+1:] {
			// If the logs agree on the term at some index, they must agree
			// on everything up to it; the highest such index covers the rest.
			for k := min(len(a.log), len(b.log)) - 1; k > 0; k-- {
				if a.log[k].Term != b.log[k].Term {
					continue
				}
				if !slices.EqualFunc(a.log[:k+1], b.log[:k+1], entriesEqual) {
					c.t.Errorf("Log Matching violated: %s and %s agree on term %d at index %d but differ before it",
						a.id, b.id, a.log[k].Term, k)
				}
				break
			}
		}
	}

	for _, s := range snaps {
		if s.role != Leader {
			continue
		}
		prev, ok := c.leaderLogs[s.id]
		if ok && prev.term == s.term &&
			(len(prev.log) > len(s.log) || !slices.EqualFunc(prev.log, s.log[:len(prev.log)], entriesEqual)) {
			c.t.Errorf("Leader Append-Only violated: %s rewrote its log in term %d", s.id, s.term)
		}
		c.leaderLogs[s.id] = leaderLog{s.term, s.log}
	}
}

// waitLeader runs the cluster until exactly one live node is leader.
func (c *testCluster) waitLeader(t *testing.T) *Raft {
	t.Helper()
	c.advanceUntil(t, 5*time.Second, func() bool { return len(c.leaders()) == 1 })
	return c.leaders()[0]
}

// one submits cmd to whichever live, connected node accepts it, and waits
// until at least want nodes have applied it at the index it got. If the
// leader loses its term first, it submits again. A cut-off leader is skipped:
// it would accept cmd but could never commit it.
func (c *testCluster) one(t *testing.T, cmd string, want int) Index {
	t.Helper()
	const attempt = 2 * time.Second
	synctest.Wait()
	for elapsed := time.Duration(0); elapsed < 10*time.Second; {
		var index Index
		accepted := false
		for i, rf := range c.nodes {
			if c.stops[i] == nil || c.tr.isPartitioned(c.ids[i]) {
				continue
			}
			if idx, _, err := rf.Start([]byte(cmd)); err == nil {
				index, accepted = idx, true
				break
			}
		}
		if !accepted {
			c.step()
			elapsed += tick
			continue
		}
		for waited := time.Duration(0); waited < attempt; waited += tick {
			if c.applied.count(index, cmd) >= want {
				return index
			}
			c.step()
		}
		elapsed += attempt
	}
	t.Fatalf("command %q not applied on %d nodes within 10s of virtual time", cmd, want)
	return 0
}

func (c *testCluster) followerOf(leader *Raft) int {
	for i, rf := range c.nodes {
		if rf != leader && c.stops[i] != nil {
			return i
		}
	}
	c.t.Fatalf("no live follower")
	return -1
}

func TestCluster_BasicAgreement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.waitLeader(t)

		var prev Index
		for k := range 3 {
			index := c.one(t, fmt.Sprint("cmd", k), 3)
			if index <= prev {
				t.Fatalf("command %d got index %d, not after %d", k, index, prev)
			}
			prev = index
		}
		want := []string{"cmd0", "cmd1", "cmd2"}
		for i := range c.nodes {
			if got := c.applied.appliedCommands(i); !slices.Equal(got, want) {
				t.Fatalf("node %d applied %q, want %q", i, got, want)
			}
		}
	})
}

func TestCluster_NoOpCommitsEntriesOfEarlierTerms(t *testing.T) {
	// A command stored on a majority but not known to be committed when its
	// leader crashes must be committed by the next leader's no-op, without
	// waiting for another client command (§8).
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader := c.waitLeader(t)
		l := slices.Index(c.nodes, leader)
		stored, missed := (l+1)%3, (l+2)%3

		// Only one follower gets the entry, in an AppendEntries sent before
		// the leader commits it, so no follower learns that it is committed.
		c.tr.setPartitioned(c.ids[missed], true)
		index, _, err := leader.Start([]byte("x"))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		synctest.Wait()
		c.crash(l)
		c.tr.setPartitioned(c.ids[missed], false)
		if got := c.applied.appliedCommands(stored); len(got) != 0 {
			t.Fatalf("setup: follower applied %q before the leader crashed", got)
		}

		// Only the follower holding the entry can win (§5.4.1), and it must
		// commit the entry through its no-op.
		c.advanceUntil(t, 5*time.Second, func() bool {
			return c.applied.count(index, "x") >= 2 &&
				slices.Equal(c.applied.appliedCommands(stored), []string{"x"}) &&
				slices.Equal(c.applied.appliedCommands(missed), []string{"x"})
		})
		if leaders := c.leaders(); len(leaders) != 1 || leaders[0] != c.nodes[stored] {
			t.Fatalf("expected %s to lead, got %v", c.ids[stored], leaders)
		}
	})
}

func TestCluster_AgreementDespiteFollowerFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader := c.waitLeader(t)
		c.one(t, "before", 3)

		f := c.followerOf(leader)
		c.tr.setPartitioned(c.ids[f], true)
		// More than one MaxEntriesPerRPC batch, so catching up takes several.
		for k := range 3 * defaultMaxEntriesPerRPC {
			c.one(t, fmt.Sprint("during", k), 2)
		}

		c.tr.setPartitioned(c.ids[f], false)
		c.one(t, "after", 3)
		if got, want := len(c.applied.appliedCommands(f)), 2+3*defaultMaxEntriesPerRPC; got != want {
			t.Fatalf("follower applied %d commands after rejoining, want %d", got, want)
		}
	})
}

func TestCluster_AgreementDespiteLeaderFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader := c.waitLeader(t)
		c.one(t, "a", 3)

		for i, rf := range c.nodes {
			if rf == leader {
				c.crash(i)
			}
		}
		c.one(t, "b", 2)
		c.one(t, "c", 2)
	})
}

func TestCluster_NoCommitWithoutMajority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 5)
		leader := c.waitLeader(t)
		c.one(t, "a", 5)

		var cut []int
		for i, rf := range c.nodes {
			if rf != leader && len(cut) < 3 {
				cut = append(cut, i)
				c.tr.setPartitioned(c.ids[i], true)
			}
		}
		index, _, err := leader.Start([]byte("lost"))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		c.advance(2 * time.Second)
		if n := c.applied.count(index, "lost"); n != 0 {
			t.Fatalf("entry without a majority applied on %d nodes", n)
		}

		for _, i := range cut {
			c.tr.setPartitioned(c.ids[i], false)
		}
		c.one(t, "b", 5)
	})
}

func TestCluster_ConcurrentStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader := c.waitLeader(t)

		const n = 5
		indices := make([]Index, n)
		var wg sync.WaitGroup
		for k := range n {
			wg.Go(func() {
				index, _, err := leader.Start([]byte(fmt.Sprint("cmd", k)))
				if err != nil {
					t.Errorf("Start: %v", err)
				}
				indices[k] = index
			})
		}
		wg.Wait()

		slices.Sort(indices)
		if len(slices.Compact(slices.Clone(indices))) != n {
			t.Fatalf("concurrent Starts got overlapping indices %v", indices)
		}
		c.advanceUntil(t, 5*time.Second, func() bool {
			for i := range c.nodes {
				if len(c.applied.appliedCommands(i)) != n {
					return false
				}
			}
			return true
		})
	})
}

func TestCluster_PartitionedLeaderRejoinsAndDropsUncommittedEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		leader1 := c.waitLeader(t)
		c.one(t, "a", 3)

		// Cut off, leader1 accepts entries it can never commit.
		c.tr.setPartitioned(leader1.id, true)
		var stale []Index
		for k := range 3 {
			index, _, err := leader1.Start([]byte(fmt.Sprint("stale", k)))
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			stale = append(stale, index)
		}
		c.advanceUntil(t, 5*time.Second, func() bool { return c.leaderOtherThan(leader1) != nil })
		c.one(t, "b", 2)

		// Now the second leader is cut off, and leader1 rejoins the third node.
		leader2 := c.leaderOtherThan(leader1)
		if leader2 == nil {
			t.Fatalf("no leader besides %s after committing b", leader1.id)
		}
		c.tr.setPartitioned(leader2.id, true)
		c.tr.setPartitioned(leader1.id, false)
		c.one(t, "c", 2)

		c.tr.setPartitioned(leader2.id, false)
		c.one(t, "d", 3)
		for k, index := range stale {
			if n := c.applied.count(index, fmt.Sprint("stale", k)); n != 0 {
				t.Fatalf("uncommitted entry of a deposed leader applied on %d nodes", n)
			}
		}
	})
}

func TestCluster_ConflictTermSkipsDivergentTermsQuickly(t *testing.T) {
	// A follower with a long run of conflicting entries is repaired in a
	// few round trips rather than one per entry (§5.3).
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		old := c.waitLeader(t)
		c.one(t, "a", 3)

		c.tr.setPartitioned(old.id, true)
		for k := range 50 {
			if _, _, err := old.Start([]byte(fmt.Sprint("stale", k))); err != nil {
				t.Fatalf("Start: %v", err)
			}
		}
		c.advanceUntil(t, 5*time.Second, func() bool { return c.leaderOtherThan(old) != nil })
		for k := range 50 {
			c.one(t, fmt.Sprint("new", k), 2)
		}
		// A leader elected now starts with nextIndex past all 50 new entries,
		// so it has to back off over old's whole conflicting run.
		leader2 := c.leaderOtherThan(old)
		if leader2 == nil {
			t.Fatalf("no leader besides %s after committing new entries", old.id)
		}
		term2, _ := leader2.GetState()
		i2 := slices.Index(c.nodes, leader2)
		c.crash(i2)
		c.start(i2)
		c.advanceUntil(t, 5*time.Second, func() bool {
			for _, rf := range c.leaders() {
				if term, _ := rf.GetState(); rf != old && term > term2 {
					return true
				}
			}
			return false
		})

		before := c.tr.appendEntriesTo(old.id)
		c.tr.setPartitioned(old.id, false)
		i := slices.Index(c.nodes, old)
		c.advanceUntil(t, 5*time.Second, func() bool { return len(c.applied.appliedCommands(i)) == 51 })
		// Repair takes about three: a mismatch past old's log, one on the
		// stale term, then the match. One per entry would take over 50.
		if n := c.tr.appendEntriesTo(old.id) - before; n > 10 {
			t.Fatalf("repairing a 50-entry conflict took %d AppendEntries", n)
		}
	})
}

func TestCluster_RestartReappliesCommittedEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newTestCluster(t, 3)
		c.waitLeader(t)
		want := []string{"a", "b", "c"}
		for _, cmd := range want {
			c.one(t, cmd, 3)
		}

		for i := range c.nodes {
			c.crash(i)
		}
		for i := range c.nodes {
			c.start(i)
		}
		c.one(t, "d", 3)
		want = append(want, "d")
		for i := range c.nodes {
			if got := c.applied.appliedCommands(i); !slices.Equal(got, want) {
				t.Fatalf("node %d applied %q after restart, want %q", i, got, want)
			}
		}
	})
}

// TestCluster_LeaderChurnKeepsSafety keeps crashing leaders at random
// moments, often before a heartbeat has carried their latest entries to
// every follower, so leaders of later terms inherit logs replicated to only
// part of the cluster. That is where committing an entry of an earlier term
// by counting replicas would break State Machine Safety (§5.4.2, Figure 8).
func TestCluster_LeaderChurnKeepsSafety(t *testing.T) {
	for seed := range uint64(10) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const n = 5
				c := newRandomizedTestCluster(t, n, seed)
				r := rand.New(rand.NewPCG(seed, 3000))
				terms := 0
				for iter := range 200 {
					for _, rf := range c.leaders() {
						_, _, _ = rf.Start([]byte(fmt.Sprint("cmd", iter)))
					}
					c.advance(tick * time.Duration(1+r.IntN(20)))

					if leaders := c.leaders(); len(leaders) == 1 && r.Float64() < 0.5 {
						c.crash(slices.Index(c.nodes, leaders[0]))
						terms++
					}
					var down []int
					for i := range n {
						if c.stops[i] == nil {
							down = append(down, i)
						}
					}
					// Keep a quorum up so the cluster can still make progress.
					if len(down) > 0 && (len(down) > n/2 || r.Float64() < 0.3) {
						c.start(down[r.IntN(len(down))])
					}
				}
				if terms < 20 {
					t.Fatalf("setup: only %d leaders crashed", terms)
				}

				for i := range n {
					if c.stops[i] == nil {
						c.start(i)
					}
				}
				c.one(t, "final", n)
			})
		})
	}
}
