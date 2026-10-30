package raft

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

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

// A duplicate grant must not invent quorum, and grants left in memory after
// stepping down must not appear as the new leader's election history.
func TestElectionStatusDuplicateGrantsAndStepdown(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C", "D", "E")
	tr := rf.transport.(*fakeTransport)
	tr.register("B", grantingPeer{})
	tr.register("C", grantingPeer{})
	setRoleAndTerm(rf, Candidate, 3, "A")
	rf.votes = map[NodeID]struct{}{"A": {}}
	args := &RequestVoteArgs{Term: 3, CandidateID: "A"}
	for range 2 {
		rf.requestVote(context.Background(), "B", args)
		if s := rf.Status().Election(); s.Role != "Candidate" || s.Votes != 2 || s.VotedFor != "A" || s.LeaderID != "" || s.Quorum != 3 {
			t.Fatalf("duplicate grant changed election: %+v", s)
		}
	}
	rf.requestVote(context.Background(), "C", args)
	if s := rf.Status().Election(); s.Role != "Leader" || s.Votes != 0 || s.LeaderID != "A" || s.VotedFor != "A" {
		t.Fatalf("leader exposes candidate tally: %+v", s)
	}
	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 4, LeaderID: "B"})
	if s := rf.Status().Election(); s.Role != "Follower" || s.Term != 4 || s.Votes != 0 || s.VotedFor != "" || s.LeaderID != "B" {
		t.Fatalf("follower invents voting history: %+v", s)
	}
}

func TestElectionStatusJSONSchema(t *testing.T) {
	s := Status{ID: "A", Role: "Follower", Quorum: 3, CommitIndex: 12, LastLogIndex: 15}
	data, err := json.Marshal(s.Election())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": "A", "role": "Follower", "term": float64(0), "leader_id": "",
		"voted_for": "", "votes": float64(0), "quorum": float64(3), "running": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("election schema: %s", data)
	}
}
