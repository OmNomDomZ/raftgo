package raft

import "time"

// Clock tells time and creates timers; tests substitute a fake one to control
// time.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer mirrors the subset of *time.Timer that Raft uses. Implementations
// must follow Go 1.23+ semantics: once Stop or Reset returns, no tick from
// the previous arming is received on C.
type Timer interface {
	C() <-chan time.Time
	Reset(d time.Duration) bool
	Stop() bool
}

// SystemClock is a Clock backed by the time package.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) Timer {
	return systemTimer{time.NewTimer(d)}
}

type systemTimer struct{ t *time.Timer }

func (s systemTimer) C() <-chan time.Time        { return s.t.C }
func (s systemTimer) Reset(d time.Duration) bool { return s.t.Reset(d) }
func (s systemTimer) Stop() bool                 { return s.t.Stop() }
