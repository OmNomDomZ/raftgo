package raft

import "testing"

func TestInspectLogCannotMutateCommittedHistory(t *testing.T) {
	rf, _ := newTestNode(t, "A")
	setRoleAndTerm(rf, Leader, 1, "A")
	index, _, err := rf.Start([]byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	s, entries := rf.InspectLog()
	if s.CommitIndex != index || s.LeaderID != "A" || s.Quorum != 1 {
		t.Fatalf("bad status: %+v", s)
	}
	entries[0].Command[0] = 'X'
	entries[0].Term = 99
	next, log := rf.InspectLog()
	if string(log[0].Command) != "original" || log[0].Term != 1 || next.CommitIndex != index {
		t.Fatal("inspection mutated committed history")
	}
}
