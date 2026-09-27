package raft

import (
	"testing"
	"time"
)

func testConfig(st Storage, id NodeID, peers ...NodeID) Config {
	return Config{
		ID:              id,
		Peers:           peers,
		Transport:       newFakeTransport(),
		Storage:         st,
		Clock:           newFakeClock(time.Unix(0, 0)),
		ElectionTimeout: fixedTimeout(time.Second),
	}
}

func newTestNode(t *testing.T, id NodeID, peers ...NodeID) (*Raft, *fakeStorage) {
	t.Helper()
	st := &fakeStorage{}
	rf, err := New(testConfig(st, id, peers...))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rf, st
}

func TestHandleRequestVote_GrantsWhenNoVoteYet(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected vote granted, got %+v", reply)
	}
	if reply.Term != 1 {
		t.Fatalf("expected reply term 1, got %d", reply.Term)
	}
	if state, _ := st.LoadState(); state.VotedFor != "B" || state.CurrentTerm != 1 {
		t.Fatalf("vote not persisted: %+v", state)
	}
}

func TestHandleRequestVote_RejectsStaleTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	rf.HandleRequestVote(&RequestVoteArgs{Term: 5, CandidateID: "B"})

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 3, CandidateID: "C"})

	if reply.VoteGranted {
		t.Fatalf("expected vote rejected for stale term, got %+v", reply)
	}
	if reply.Term != 5 {
		t.Fatalf("expected reply term 5, got %d", reply.Term)
	}
}

func TestHandleRequestVote_RejectsSecondVoteInSameTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	first := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})
	if !first.VoteGranted {
		t.Fatalf("setup: expected first vote granted")
	}

	second := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "C"})

	if second.VoteGranted {
		t.Fatalf("expected second vote in same term rejected, got %+v", second)
	}
}

func TestHandleRequestVote_GrantsSameCandidateAgain(t *testing.T) {
	// A retried RequestVote must not be rejected (Figure 2: votedFor == candidateId).
	rf, _ := newTestNode(t, "A", "B")
	rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected repeat vote for same candidate granted, got %+v", reply)
	}
}

func TestHandleRequestVote_HigherTermStepsDownAndGrants(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	rf.mu.Lock()
	rf.role = Leader
	rf.currentTerm = 3
	rf.votedFor = "A"
	rf.mu.Unlock()

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 4, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected vote granted after stepping down, got %+v", reply)
	}
	term, isLeader := rf.GetState()
	if isLeader {
		t.Fatalf("expected node to step down from Leader")
	}
	if term != 4 {
		t.Fatalf("expected term 4, got %d", term)
	}
}

func TestHandleRequestVote_PersistFailureDeclinesVote(t *testing.T) {
	rf, st := newTestNode(t, "A", "B")
	st.setFailSave(true)

	reply := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})

	if reply.VoteGranted {
		t.Fatalf("expected vote declined when persistence fails, got %+v", reply)
	}
}

func TestHandleAppendEntries_RejectsStaleTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	rf.mu.Lock()
	rf.currentTerm = 5
	rf.mu.Unlock()

	reply := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 3, LeaderID: "B"})

	if reply.Success {
		t.Fatalf("expected heartbeat from stale term rejected, got %+v", reply)
	}
	if reply.Term != 5 {
		t.Fatalf("expected reply term 5, got %d", reply.Term)
	}
}

func TestHandleAppendEntries_AcceptsAndStepsDownFromCandidate(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	rf.mu.Lock()
	rf.currentTerm = 2
	rf.role = Candidate
	rf.votedFor = "A"
	rf.mu.Unlock()

	reply := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected same-term heartbeat accepted, got %+v", reply)
	}
	_, isLeader := rf.GetState()
	if isLeader {
		t.Fatalf("node should not be leader")
	}
	rf.mu.Lock()
	role := rf.role
	rf.mu.Unlock()
	if role != Follower {
		t.Fatalf("expected candidate to step down to Follower, got %v", role)
	}
}

func TestHandleAppendEntries_HigherTermStepsDownLeader(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	rf.mu.Lock()
	rf.role = Leader
	rf.currentTerm = 1
	rf.mu.Unlock()

	reply := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected heartbeat accepted, got %+v", reply)
	}
	term, isLeader := rf.GetState()
	if isLeader || term != 2 {
		t.Fatalf("expected follower at term 2, got term=%d isLeader=%v", term, isLeader)
	}
}

func TestGetStateAndStart_ReflectRoleAndTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")

	term, isLeader := rf.GetState()
	if term != 0 || isLeader {
		t.Fatalf("fresh node should start at term 0, not leader; got term=%d isLeader=%v", term, isLeader)
	}

	_, _, isLeader = rf.Start([]byte("cmd"))
	if isLeader {
		t.Fatalf("non-leader Start must report isLeader=false")
	}

	rf.mu.Lock()
	rf.role = Leader
	rf.currentTerm = 7
	rf.mu.Unlock()

	index, term, isLeader := rf.Start([]byte("cmd"))
	if !isLeader || term != 7 {
		t.Fatalf("expected leader Start to report isLeader=true term=7, got index=%d term=%d isLeader=%v", index, term, isLeader)
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing ID", func(c *Config) { c.ID = "" }},
		{"peers contain self", func(c *Config) { c.Peers = append(c.Peers, c.ID) }},
		{"missing transport", func(c *Config) { c.Transport = nil }},
		{"missing storage", func(c *Config) { c.Storage = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(&fakeStorage{}, "A", "B")
			tt.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestNew_LoadFailureIsReported(t *testing.T) {
	if _, err := New(testConfig(&fakeStorage{failLoad: true}, "A", "B")); err == nil {
		t.Fatalf("expected New to fail when state cannot be loaded")
	}
}

func TestNew_DoesNotAliasPeers(t *testing.T) {
	peers := []NodeID{"B", "C"}
	rf, err := New(testConfig(&fakeStorage{}, "A", peers...))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	peers[0] = "X"
	if rf.peers[0] != "B" {
		t.Fatalf("caller's slice mutation leaked into the node: %v", rf.peers)
	}
}

func TestRestart_RestoresTermAndVote(t *testing.T) {
	// A restarted node must not vote twice in one term (§5.1, Figure 2).
	rf, st := newTestNode(t, "A", "B", "C")
	if !rf.HandleRequestVote(&RequestVoteArgs{Term: 4, CandidateID: "B"}).VoteGranted {
		t.Fatalf("setup: expected vote for B granted")
	}

	restarted, err := New(testConfig(st, "A", "B", "C"))
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}

	if term, _ := restarted.GetState(); term != 4 {
		t.Fatalf("expected restored term 4, got %d", term)
	}
	if reply := restarted.HandleRequestVote(&RequestVoteArgs{Term: 4, CandidateID: "C"}); reply.VoteGranted {
		t.Fatalf("restarted node voted twice in term 4: %+v", reply)
	}
	if reply := restarted.HandleRequestVote(&RequestVoteArgs{Term: 4, CandidateID: "B"}); !reply.VoteGranted {
		t.Fatalf("restarted node must still grant a retried vote to B: %+v", reply)
	}
}
