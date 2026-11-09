package raft

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock позволяет всем узлам теста использовать одно управляемое время.
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

// Продвигаем время и выдаём сигналы таймеров в порядке их сроков.
func (c *fakeClock) Advance(d time.Duration) {
	// Сигналы таймера не должны пересекаться со Stop или Reset.
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

func fixedTimeout(d time.Duration) func() time.Duration {
	return func() time.Duration { return d }
}

// Возвращаем заданные таймауты по очереди, затем повторяем последний.
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

// Проверяем, что тестовый таймаут остаётся в допустимом диапазоне.
func boundedTimeout(t testing.TB, lo, hi time.Duration, f func() time.Duration) func() time.Duration {
	if f == nil {
		return nil
	}
	return func() time.Duration {
		d := f()
		if d < lo || d >= hi {
			t.Errorf("scripted election timeout %v outside [%v, %v)", d, lo, hi)
		}
		return d
	}
}

type fakeStorage struct {
	mu          sync.Mutex
	state       PersistentState
	log         []LogEntry
	saves       int
	logWrites   int
	failSave    bool
	failLoad    bool
	failLoadLog bool
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

func (s *fakeStorage) StoreLog(from Index, entries []LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return errors.New("fake storage: store log failed")
	}
	if from == 0 || from > Index(len(s.log))+1 {
		return fmt.Errorf("fake storage: store from %d, log has %d entries", from, len(s.log))
	}
	s.log = append(slices.Clone(s.log[:from-1]), entries...)
	s.logWrites++
	return nil
}

func (s *fakeStorage) LoadLog() ([]LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoadLog {
		return nil, errors.New("fake storage: load log failed")
	}
	return slices.Clone(s.log), nil
}

func (s *fakeStorage) storedLog() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

func (s *fakeStorage) logWriteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logWrites
}

// fakeTransport передаёт RPC в памяти и позволяет моделировать потери и разделения сети.
type fakeTransport struct {
	mu            sync.Mutex
	nodes         map[NodeID]RPCHandler
	partitioned   map[NodeID]bool
	drop          func() bool
	delay         func() time.Duration
	clock         *fakeClock
	closed        chan struct{}
	appendEntries map[NodeID]int
	late          int
}

var (
	errPartitioned = errors.New("fake transport: partitioned")
	errUnknownNode = errors.New("fake transport: unknown node")
	errDropped     = errors.New("fake transport: message dropped")
	errClosed      = errors.New("fake transport: closed")
)

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		nodes:         map[NodeID]RPCHandler{},
		partitioned:   map[NodeID]bool{},
		closed:        make(chan struct{}),
		appendEntries: map[NodeID]int{},
	}
}

func (f *fakeTransport) setDelay(clock *fakeClock, delay func() time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock, f.delay = clock, delay
}

func (f *fakeTransport) close() {
	close(f.closed)
}

func (f *fakeTransport) lateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.late
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

func (f *fakeTransport) appendEntriesTo(id NodeID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.appendEntries[id]
}

func (f *fakeTransport) isPartitioned(id NodeID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.partitioned[id]
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

func (f *fakeTransport) linked(from, to NodeID) (RPCHandler, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.nodes[to]
	return h, ok && !f.partitioned[from] && !f.partitioned[to]
}

func (f *fakeTransport) travel(clock *fakeClock, delay func() time.Duration) bool {
	f.mu.Lock()
	d := delay()
	f.mu.Unlock()
	if d <= 0 {
		return true
	}
	timer := clock.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C():
		return true
	case <-f.closed:
		return false
	}
}

// Доставляем запрос и ответ с заданной задержкой; отменённый запрос ещё может дойти.
func roundTrip[Reply any](ctx context.Context, f *fakeTransport, from, to NodeID, handle func(RPCHandler) (*Reply, error)) (*Reply, error) {
	h, err := f.route(from, to)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	clock, delay := f.clock, f.delay
	f.mu.Unlock()

	if delay == nil {
		reply, err := handle(h)
		if err != nil {
			return nil, err
		}
		if f.dropped() {
			return nil, errDropped
		}
		return reply, nil
	}

	type result struct {
		reply *Reply
		err   error
	}
	res := make(chan result, 1)
	go func() {
		if !f.travel(clock, delay) {
			res <- result{nil, errClosed}
			return
		}
		h, ok := f.linked(from, to)
		if !ok {
			res <- result{nil, errPartitioned}
			return
		}
		if ctx.Err() != nil {
			f.mu.Lock()
			f.late++
			f.mu.Unlock()
		}
		reply, err := handle(h)
		if err != nil {
			res <- result{nil, err}
			return
		}
		if !f.travel(clock, delay) {
			res <- result{nil, errClosed}
			return
		}
		if _, ok := f.linked(to, from); !ok || f.dropped() {
			res <- result{nil, errDropped}
			return
		}
		res <- result{reply, nil}
	}()
	select {
	case r := <-res:
		return r.reply, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeTransport) RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error) {
	return roundTrip(ctx, f, args.CandidateID, target, func(h RPCHandler) (*RequestVoteReply, error) {
		return h.HandleRequestVote(args)
	})
}

func (f *fakeTransport) AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return roundTrip(ctx, f, args.LeaderID, target, func(h RPCHandler) (*AppendEntriesReply, error) {
		f.mu.Lock()
		f.appendEntries[target]++
		f.mu.Unlock()
		return h.HandleAppendEntries(args)
	})
}

type stubPeer struct{ term Term }

func (p stubPeer) HandleRequestVote(*RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: p.term}, nil
}

func (p stubPeer) HandleAppendEntries(*AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: p.term}, nil
}

type grantingPeer struct{}

func (grantingPeer) HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: args.Term, VoteGranted: true}, nil
}

func (grantingPeer) HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: args.Term, Success: true}, nil
}

type wrongTermPeer struct{ term Term }

func (p wrongTermPeer) HandleRequestVote(*RequestVoteArgs) (*RequestVoteReply, error) {
	return &RequestVoteReply{Term: p.term, VoteGranted: true}, nil
}

func (p wrongTermPeer) HandleAppendEntries(*AppendEntriesArgs) (*AppendEntriesReply, error) {
	return &AppendEntriesReply{Term: p.term, Success: true}, nil
}

// Удерживаем RPC до отмены контекста и записываем его term.
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

// Нарушаем контракт транспорта: не возвращаем ни ответ, ни ошибку.
type nilTransport struct{}

func (nilTransport) RequestVote(context.Context, NodeID, *RequestVoteArgs) (*RequestVoteReply, error) {
	return nil, nil
}

func (nilTransport) AppendEntries(context.Context, NodeID, *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return nil, nil
}
