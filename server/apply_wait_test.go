package server

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/raft"
)

func TestWaitAppliedBeforeSubscriptionAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		applied raft.Index
	}{
		{"not_applied", 0},
		{"at_target", 4},
		{"past_target", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := &API{applied: tc.applied, changed: make(chan struct{})}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				// Existing progress wins even if cancellation is already observable.
				err := api.waitApplied(ctx, 4)
				if tc.applied >= 4 {
					if err != nil {
						t.Fatalf("already applied: %v", err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("unapplied index: %v", err)
				}
			})
		})
	}
}

func TestWaitAppliedBroadcastProgressAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &API{changed: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		results := make([]chan error, 3)
		for i, index := range []raft.Index{2, 2, 3} {
			results[i] = make(chan error, 1)
			go func() { results[i] <- api.waitApplied(ctx, index) }()
		}
		synctest.Wait()
		progress := func(index raft.Index) {
			api.mu.Lock()
			api.applied = index
			close(api.changed)
			api.changed = make(chan struct{})
			api.mu.Unlock()
			synctest.Wait()
		}
		assertPending := func(result <-chan error) {
			t.Helper()
			select {
			case err := <-result:
				t.Fatalf("wait ended before target application: %v", err)
			default:
			}
		}
		progress(1)
		for _, result := range results {
			assertPending(result)
		}
		progress(2)
		for _, result := range results[:2] {
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatal("application failed to wake every matching waiter")
			}
		}
		assertPending(results[2])
		cancel()
		synctest.Wait()
		if err := <-results[2]; !errors.Is(err, context.Canceled) {
			t.Fatalf("pending wait cancellation: %v", err)
		}
		deadline, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := api.waitApplied(deadline, 3); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pending wait deadline: %v", err)
		}
	})
}
