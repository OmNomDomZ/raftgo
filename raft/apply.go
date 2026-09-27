package raft

import "context"

// ApplyMsg delivers one committed command to the state machine.
//
// Indices on ApplyCh strictly increase but may have gaps: entries that carry
// no command, such as a new leader's no-op, are committed but not sent. A
// restarted node starts over from index 1, so the state machine must be
// rebuilt from scratch, not resumed.
type ApplyMsg struct {
	Index   Index
	Term    Term
	Command []byte // shared: the receiver must not modify it
}

// wakeApplierLocked tells the applier that commitIndex has advanced.
func (rf *Raft) wakeApplierLocked() {
	select {
	case rf.applyWake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// apply runs for the node's lifetime and sends committed commands to
// ApplyCh. Sending outside mu keeps a slow consumer from stalling the
// protocol, and lets the consumer call Start while handling a message.
func (rf *Raft) apply(ctx context.Context) {
	var applied Index
	for {
		select {
		case <-ctx.Done():
			return
		case <-rf.applyWake:
		}

		rf.mu.Lock()
		first := applied + 1
		// Committed entries never change (State Machine Safety, §5.4.3), so a
		// copy taken now stays valid while it is sent outside mu.
		entries := rf.entriesLocked(first, rf.commitIndex+1)
		rf.mu.Unlock()

		for i, e := range entries {
			if e.Kind != EntryCommand {
				continue
			}
			msg := ApplyMsg{Index: first + Index(i), Term: e.Term, Command: e.Command}
			select {
			case rf.applyCh <- msg:
			case <-ctx.Done():
				return
			}
		}
		applied += Index(len(entries))
	}
}
