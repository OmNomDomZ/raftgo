package raft

import (
	"errors"
	"testing"
	"time"
)

func TestHandleRequestVote_GrantsWhenNoVoteYet(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")

	reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"})

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

func TestHandleRequestVote_EmptyCandidateIsNotAVote(t *testing.T) {
	// votedFor=="" means "no vote", so a grant to "" would not stop a second
	// vote in the same term.
	rf, st := newTestNode(t, "A", "B", "C")

	for _, args := range []*RequestVoteArgs{nil, {Term: 1}} {
		if reply, err := rf.HandleRequestVote(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleRequestVote(%+v) = %+v, %v; want no reply and ErrInvalidArgs", args, reply, err)
		}
	}
	if role, term := roleAndTerm(rf); role != Follower || term != 0 || votedFor(rf) != "" || st.saveCount() != 0 {
		t.Fatalf("invalid request changed state: %v in term %d, votedFor %q, %d saves",
			role, term, votedFor(rf), st.saveCount())
	}
	if reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"}); !reply.VoteGranted {
		t.Fatalf("expected the first real candidate to get the vote, got %+v", reply)
	}
}

func TestHandleAppendEntries_EmptyLeaderIsRejected(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")

	for _, args := range []*AppendEntriesArgs{nil, {Term: 1}} {
		if reply, err := rf.HandleAppendEntries(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleAppendEntries(%+v) = %+v, %v; want no reply and ErrInvalidArgs", args, reply, err)
		}
	}
	if _, term := roleAndTerm(rf); term != 0 {
		t.Fatalf("invalid request changed the term to %d", term)
	}
}

func TestHandleRequestVote_RejectsStaleTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 5, CandidateID: "B"})

	reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 3, CandidateID: "C"})

	if reply.VoteGranted {
		t.Fatalf("expected vote rejected for stale term, got %+v", reply)
	}
	if reply.Term != 5 {
		t.Fatalf("expected reply term 5, got %d", reply.Term)
	}
}

func TestHandleRequestVote_RejectsSecondVoteInSameTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	first := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"})
	if !first.VoteGranted {
		t.Fatalf("setup: expected first vote granted")
	}

	second := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "C"})

	if second.VoteGranted {
		t.Fatalf("expected second vote in same term rejected, got %+v", second)
	}
}

func TestHandleRequestVote_GrantsSameCandidateAgain(t *testing.T) {
	// A retried RequestVote must not be rejected (Figure 2: votedFor == candidateId).
	rf, _ := newTestNode(t, "A", "B")
	mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"})

	reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"})

	if !reply.VoteGranted {
		t.Fatalf("expected repeat vote for same candidate granted, got %+v", reply)
	}
}

func TestHandleRequestVote_HigherTermStepsDownAndGrants(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 3, "A")

	reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 4, CandidateID: "B"})

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

func TestHandleRequestVote_PersistFailureSendsNoReply(t *testing.T) {
	// A vote that is not durable must not be granted, and a refusal would be
	// a lie too, so the node answers nothing.
	rf, st := newTestNode(t, "A", "B")
	st.setFailSave(true)

	reply, err := rf.HandleRequestVote(&RequestVoteArgs{Term: 1, CandidateID: "B"})

	if err == nil || reply != nil {
		t.Fatalf("expected an error and no reply when persistence fails, got reply=%+v err=%v", reply, err)
	}
	if got := votedFor(rf); got != "" {
		t.Fatalf("non-durable vote kept in memory: votedFor=%q", got)
	}
}

func TestHandleAppendEntries_RejectsStaleTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	setRoleAndTerm(rf, Follower, 5, "")

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 3, LeaderID: "B"})

	if reply.Success {
		t.Fatalf("expected heartbeat from stale term rejected, got %+v", reply)
	}
	if reply.Term != 5 {
		t.Fatalf("expected reply term 5, got %d", reply.Term)
	}
}

func TestHandleAppendEntries_AcceptsAndStepsDownFromCandidate(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	setRoleAndTerm(rf, Candidate, 2, "A")

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected same-term heartbeat accepted, got %+v", reply)
	}
	if role, _ := roleAndTerm(rf); role != Follower {
		t.Fatalf("expected candidate to step down to Follower, got %v", role)
	}
}

func TestHandleAppendEntries_HigherTermStepsDownLeader(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	setRoleAndTerm(rf, Leader, 1, "A")

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if !reply.Success {
		t.Fatalf("expected heartbeat accepted, got %+v", reply)
	}
	term, isLeader := rf.GetState()
	if isLeader || term != 2 {
		t.Fatalf("expected follower at term 2, got term=%d isLeader=%v", term, isLeader)
	}
}

func TestGetState_ReflectsRoleAndTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")

	if term, isLeader := rf.GetState(); term != 0 || isLeader {
		t.Fatalf("fresh node should start at term 0, not leader; got term=%d isLeader=%v", term, isLeader)
	}

	setRoleAndTerm(rf, Leader, 7, "A")

	if term, isLeader := rf.GetState(); term != 7 || !isLeader {
		t.Fatalf("expected leader of term 7, got term=%d isLeader=%v", term, isLeader)
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing ID", func(c *Config) { c.ID = "" }},
		{"peers contain self", func(c *Config) { c.Peers = append(c.Peers, c.ID) }},
		{"duplicate peers", func(c *Config) { c.Peers = []NodeID{"B", "C", "B"} }},
		{"empty peer ID", func(c *Config) { c.Peers = append(c.Peers, "") }},
		{"missing transport", func(c *Config) { c.Transport = nil }},
		{"missing storage", func(c *Config) { c.Storage = nil }},
		{"negative heartbeat interval", func(c *Config) { c.HeartbeatInterval = -time.Millisecond }},
		{"negative RPC timeout", func(c *Config) { c.RPCTimeout = -time.Millisecond }},
		{"heartbeat not below default election timeout", func(c *Config) {
			c.ElectionTimeout, c.ElectionTimeoutMin = nil, 0
			c.HeartbeatInterval = defaultElectionMin
		}},
		{"default heartbeat not below custom election timeout", func(c *Config) {
			c.ElectionTimeout = fixedTimeout(defaultHeartbeatInterval)
			c.ElectionTimeoutMin = defaultHeartbeatInterval
		}},
		{"heartbeat not below custom election timeout", func(c *Config) {
			c.HeartbeatInterval = c.ElectionTimeoutMin
		}},
		{"custom election timeout without its minimum", func(c *Config) { c.ElectionTimeoutMin = 0 }},
		{"election timeout minimum without custom election timeout", func(c *Config) { c.ElectionTimeout = nil }},
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
	if rf.peers[0].id != "B" {
		t.Fatalf("caller's slice mutation leaked into the node: %v", rf.peers)
	}
}

func TestRestart_RestoresTermAndVote(t *testing.T) {
	// A restarted node must not vote twice in one term (§5.1, Figure 2).
	rf, st := newTestNode(t, "A", "B", "C")
	if !mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 4, CandidateID: "B"}).VoteGranted {
		t.Fatalf("setup: expected vote for B granted")
	}

	restarted, err := New(testConfig(st, "A", "B", "C"))
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}

	if term, _ := restarted.GetState(); term != 4 {
		t.Fatalf("expected restored term 4, got %d", term)
	}
	if reply := mustHandleRequestVote(t, restarted, &RequestVoteArgs{Term: 4, CandidateID: "C"}); reply.VoteGranted {
		t.Fatalf("restarted node voted twice in term 4: %+v", reply)
	}
	if reply := mustHandleRequestVote(t, restarted, &RequestVoteArgs{Term: 4, CandidateID: "B"}); !reply.VoteGranted {
		t.Fatalf("restarted node must still grant a retried vote to B: %+v", reply)
	}
}

func TestHandleRequestVote_RetryDoesNotRewriteStorage(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"})
	saves := st.saveCount()

	if !mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 1, CandidateID: "B"}).VoteGranted {
		t.Fatalf("expected retried vote granted")
	}
	if got := st.saveCount(); got != saves {
		t.Fatalf("retried vote for the same candidate wrote storage again: %d -> %d saves", saves, got)
	}
}

func TestHandleAppendEntries_SecondLeaderInTermIsRejected(t *testing.T) {
	// Election Safety is already broken if this happens, but a message from
	// the network must not crash the node.
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 3, "A")

	if reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 3, LeaderID: "B"}); reply.Success || reply.Term != 3 {
		t.Fatalf("expected Success=false with term 3, got %+v", reply)
	}
	if role, term := roleAndTerm(rf); role != Leader || term != 3 {
		t.Fatalf("expected to stay leader of term 3, got %v in term %d", role, term)
	}
}

func TestAdoptHigherTerm_NotHigherIsFatal(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 3, "B")

	rf.mu.Lock()
	defer rf.mu.Unlock()
	mustBeFatal(t, func() { rf.adoptHigherTermLocked(3) })
	if rf.votedFor != "B" {
		t.Fatalf("vote in term 3 was cleared: votedFor=%q", rf.votedFor)
	}
}
