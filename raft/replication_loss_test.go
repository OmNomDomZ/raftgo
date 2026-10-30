package raft

import (
	"context"
	"slices"
	"testing"
)

func TestSendAppendEntries_LostReplyRetriesStoredEntry(t *testing.T) {
	leader, _ := newTestNode(t, "A", "B", "C")
	follower, st := newTestNode(t, "B", "A", "C")
	setRoleAndTerm(leader, Leader, 1, "A")
	tr := leader.transport.(*fakeTransport)
	tr.register("B", follower)
	index, term, err := leader.Start([]byte("повтор"))
	if err != nil {
		t.Fatal(err)
	}
	leader.mu.Lock()
	args, round := leader.appendEntriesArgsLocked("B")
	leader.mu.Unlock()

	// Deliver the request, then lose only its reply. The follower has stored
	// it, but the leader must not count the missing acknowledgement.
	calls := 0
	tr.setDrop(func() bool { calls++; return calls == 2 })
	if leader.sendAppendEntries(context.Background(), "B", args, round) {
		t.Fatal("lost reply requested an immediate retry")
	}
	if calls != 2 || len(st.storedLog()) != int(index) {
		t.Fatal("setup: request was not stored before losing its reply")
	}
	if commitIndex(leader) != 0 || commitIndex(follower) != 0 {
		t.Fatal("lost acknowledgement committed an entry")
	}
	writes := st.logWriteCount()
	tr.setDrop(nil)
	leader.sendAppendEntries(context.Background(), "B", args, round)
	if commitIndex(leader) != index || st.logWriteCount() != writes {
		t.Fatal("retry failed to commit or rewrote the stored entry")
	}
	leader.mu.Lock()
	heartbeat, round := leader.appendEntriesArgsLocked("B")
	leader.mu.Unlock()
	leader.sendAppendEntries(context.Background(), "B", heartbeat, round)
	_, log := follower.InspectLog()
	if commitIndex(follower) != index || len(log) != 1 || !entriesEqual(log[0], cmdEntry(term, "повтор")) {
		t.Fatalf("retry changed the command or its position: %+v", log)
	}

	// A delayed duplicate before the commit announcement cannot retract the
	// commit or truncate an already acknowledged prefix.
	mustHandleAppendEntries(t, follower, args)
	_, after := follower.InspectLog()
	if commitIndex(follower) != index || !slices.EqualFunc(log, after, entriesEqual) {
		t.Fatal("delayed duplicate changed the committed log")
	}
}
