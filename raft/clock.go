package raft

import "time"

// Clock позволяет подменить реальные часы управляемыми часами в тестах.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer не должен выдавать старые сигналы после Stop или Reset.
type Timer interface {
	C() <-chan time.Time
	Reset(d time.Duration) bool
	Stop() bool
}

// SystemClock использует стандартные часы и таймеры Go.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) Timer {
	return systemTimer{time.NewTimer(d)}
}

type systemTimer struct{ t *time.Timer }

func (s systemTimer) C() <-chan time.Time        { return s.t.C }
func (s systemTimer) Reset(d time.Duration) bool { return s.t.Reset(d) }
func (s systemTimer) Stop() bool                 { return s.t.Stop() }
