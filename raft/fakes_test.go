package raft

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// fakeClock is shared by all nodes of a test cluster so a test can advance
// virtual time for all of them at once. Like time.Timer since Go 1.23, a tick
// that is never read is not re-sent, and Stop/Reset discard a pending tick.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timerEntry
}

type timerEntry struct {
	timer  *fakeTimer
	fireAt time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, c: make(chan time.Time, 1)}
	c.timers = append(c.timers, &timerEntry{timer: t, fireAt: c.now.Add(d)})
	return t
}

// Advance moves virtual time forward by d and fires every due timer in
// deadline order.
func (c *fakeClock) Advance(d time.Duration) {
	// Sends happen under mu so they cannot interleave with Stop/Reset.
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)

	var due []*timerEntry
	c.timers = slices.DeleteFunc(c.timers, func(e *timerEntry) bool {
		if e.fireAt.After(c.now) {
			return false
		}
		due = append(due, e)
		return true
	})
	slices.SortStableFunc(due, func(a, b *timerEntry) int { return a.fireAt.Compare(b.fireAt) })
	for _, e := range due {
		select {
		case e.timer.c <- c.now:
		default:
		}
	}
}

func (c *fakeClock) stopLocked(t *fakeTimer) bool {
	select {
	case <-t.c:
	default:
	}
	n := len(c.timers)
	c.timers = slices.DeleteFunc(c.timers, func(e *timerEntry) bool { return e.timer == t })
	return len(c.timers) < n
}

func (c *fakeClock) stopTimer(t *fakeTimer) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopLocked(t)
}

func (c *fakeClock) resetTimer(t *fakeTimer, d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	active := c.stopLocked(t)
	c.timers = append(c.timers, &timerEntry{timer: t, fireAt: c.now.Add(d)})
	return active
}

type fakeTimer struct {
	clock *fakeClock
	c     chan time.Time
}

func (t *fakeTimer) C() <-chan time.Time        { return t.c }
func (t *fakeTimer) Stop() bool                 { return t.clock.stopTimer(t) }
func (t *fakeTimer) Reset(d time.Duration) bool { return t.clock.resetTimer(t, d) }

// fixedTimeout lets a test decide which node's election timer fires first.
func fixedTimeout(d time.Duration) func() time.Duration {
	return func() time.Duration { return d }
}

// seqTimeout returns ds in order, then repeats the last one, to script an
// unlucky sequence of randomized election timeouts.
func seqTimeout(ds ...time.Duration) func() time.Duration {
	var mu sync.Mutex
	i := 0
	return func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		d := ds[min(i, len(ds)-1)]
		i++
		return d
	}
}

type fakeStorage struct {
	mu       sync.Mutex
	state    PersistentState
	failSave bool
	failLoad bool
}

func (s *fakeStorage) setFailSave(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSave = fail
}

func (s *fakeStorage) SaveState(st PersistentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return errors.New("fake storage: save failed")
	}
	s.state = st
	return nil
}

func (s *fakeStorage) LoadState() (PersistentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad {
		return PersistentState{}, errors.New("fake storage: load failed")
	}
	return s.state, nil
}

// fakeTransport calls the target node's handlers synchronously in the
// caller's goroutine. A partitioned node can neither send nor receive.
type fakeTransport struct {
	mu          sync.Mutex
	nodes       map[NodeID]RPCHandler
	partitioned map[NodeID]bool
}

var (
	errPartitioned = errors.New("fake transport: partitioned")
	errUnknownNode = errors.New("fake transport: unknown node")
)

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		nodes:       map[NodeID]RPCHandler{},
		partitioned: map[NodeID]bool{},
	}
}

func (f *fakeTransport) register(id NodeID, h RPCHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[id] = h
}

func (f *fakeTransport) setPartitioned(id NodeID, partitioned bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partitioned[id] = partitioned
}

func (f *fakeTransport) route(from, to NodeID) (RPCHandler, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.partitioned[from] || f.partitioned[to] {
		return nil, errPartitioned
	}
	h, ok := f.nodes[to]
	if !ok {
		return nil, errUnknownNode
	}
	return h, nil
}

func (f *fakeTransport) RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error) {
	h, err := f.route(args.CandidateID, target)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.HandleRequestVote(args), nil
}

func (f *fakeTransport) AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	h, err := f.route(args.LeaderID, target)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.HandleAppendEntries(args), nil
}

// stubPeer answers every RPC with a fixed term and no vote/success, standing
// in for a peer that has moved to a higher term.
type stubPeer struct{ term Term }

func (p stubPeer) HandleRequestVote(*RequestVoteArgs) *RequestVoteReply {
	return &RequestVoteReply{Term: p.term}
}

func (p stubPeer) HandleAppendEntries(*AppendEntriesArgs) *AppendEntriesReply {
	return &AppendEntriesReply{Term: p.term}
}
