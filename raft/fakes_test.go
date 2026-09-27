package raft

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
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

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
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

// boundedTimeout fails t whenever f returns a duration outside [lo, hi).
// HeartbeatInterval is only validated against the minimum, so a scripted
// timeout outside the configured bounds would test an unsupported setup. A
// nil f stays nil, so the node draws its own randomized timeouts.
func boundedTimeout(t testing.TB, lo, hi time.Duration, f func() time.Duration) func() time.Duration {
	if f == nil {
		return nil
	}
	return func() time.Duration {
		d := f()
		if d < lo || d >= hi {
			// Errorf, not Fatalf: this runs on the nodes' goroutines.
			t.Errorf("scripted election timeout %v outside [%v, %v)", d, lo, hi)
		}
		return d
	}
}

type fakeStorage struct {
	mu       sync.Mutex
	state    PersistentState
	saves    int // successful SaveState calls
	failSave bool
	failLoad bool
}

func (s *fakeStorage) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
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
	s.saves++
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
// caller's goroutine. A partitioned node can neither send nor receive. If a
// drop function is set, each request and each reply is lost when it returns
// true; a lost reply means the target has already acted on the request.
type fakeTransport struct {
	mu          sync.Mutex
	nodes       map[NodeID]RPCHandler
	partitioned map[NodeID]bool
	drop        func() bool
}

var (
	errPartitioned = errors.New("fake transport: partitioned")
	errUnknownNode = errors.New("fake transport: unknown node")
	errDropped     = errors.New("fake transport: message dropped")
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

func (f *fakeTransport) setDrop(drop func() bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drop = drop
}

func (f *fakeTransport) dropped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drop != nil && f.drop()
}

func (f *fakeTransport) route(from, to NodeID) (RPCHandler, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.partitioned[from] || f.partitioned[to] {
		return nil, errPartitioned
	}
	if f.drop != nil && f.drop() {
		return nil, errDropped
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
	reply, err := h.HandleRequestVote(args)
	if err != nil {
		return nil, err
	}
	if f.dropped() {
		return nil, errDropped
	}
	return reply, nil
}

func (f *fakeTransport) AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	h, err := f.route(args.LeaderID, target)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reply, err := h.HandleAppendEntries(args)
	if err != nil {
		return nil, err
	}
	if f.dropped() {
		return nil, errDropped
	}
	return reply, nil
}

// stubPeer answers every RPC with a fixed term and no vote/success, standing
// in for a peer that has moved to a higher term.
type stubPeer struct{ term Term }

func (p stubPeer) HandleRequestVote(*RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: p.term}, nil
}

func (p stubPeer) HandleAppendEntries(*AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: p.term}, nil
}

// grantingPeer grants every vote in the requested term.
type grantingPeer struct{}

func (grantingPeer) HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: args.Term, VoteGranted: true}, nil
}

func (grantingPeer) HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: args.Term, Success: true}, nil
}

// wrongTermPeer grants every vote and acknowledges every heartbeat, but in
// a fixed term whatever the request's, which no correct peer does.
type wrongTermPeer struct{ term Term }

func (p wrongTermPeer) HandleRequestVote(*RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: p.term, VoteGranted: true}, nil
}

func (p wrongTermPeer) HandleAppendEntries(*AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: p.term, Success: true}, nil
}

// blockingTransport holds every RPC until its context is done, then reports
// the RPC's term on released.
type blockingTransport struct{ released chan Term }

func newBlockingTransport() blockingTransport {
	return blockingTransport{released: make(chan Term, 16)}
}

func (b blockingTransport) RequestVote(ctx context.Context, _ NodeID, args *RequestVoteArgs) (*RequestVoteReply, error) {
	<-ctx.Done()
	b.released <- args.Term
	return nil, ctx.Err()
}

func (b blockingTransport) AppendEntries(ctx context.Context, _ NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	<-ctx.Done()
	b.released <- args.Term
	return nil, ctx.Err()
}

// nilTransport violates the Transport contract by returning neither a reply
// nor an error.
type nilTransport struct{}

func (nilTransport) RequestVote(context.Context, NodeID, *RequestVoteArgs) (*RequestVoteReply, error) {
	return nil, nil
}

func (nilTransport) AppendEntries(context.Context, NodeID, *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return nil, nil
}
