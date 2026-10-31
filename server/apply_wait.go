package server

import (
	"context"

	"raftgo/raft"
)

// waitApplied checks progress and subscribes to its next change under the same
// lock so application between the check and the wait cannot lose a notification.
func (a *API) waitApplied(ctx context.Context, index raft.Index) error {
	for {
		a.mu.Lock()
		applied, changed := a.applied, a.changed
		a.mu.Unlock()
		if applied >= index {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
