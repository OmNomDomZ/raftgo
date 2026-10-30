package raft

import (
	"errors"
	"testing"
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

func TestHandleRequestVote_InvalidArgsGetNoReply(t *testing.T) {
	// votedFor=="" means "no vote", so a grant to "" would not stop a second
	// vote in the same term. No correct candidate campaigns in term 0 or
	// sends RPCs to itself.
	rf, st := newTestNode(t, "A", "B", "C")

	invalid := []*RequestVoteArgs{
		nil,
		{Term: 1},
		{Term: 1, CandidateID: "A"},
		{Term: 0, CandidateID: "B"},
		{Term: 1, CandidateID: "B", LastLogIndex: 1, LastLogTerm: 1},
	}
	for _, args := range invalid {
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

func TestHandleAppendEntries_InvalidArgsGetNoReply(t *testing.T) {
	// No correct leader has term 0 or sends RPCs to itself; acknowledging
	// such a "leader" would also hold back elections.
	rf, st := newTestNode(t, "A", "B")

	invalid := []*AppendEntriesArgs{
		nil,
		{Term: 1},
		{Term: 1, LeaderID: "A"},
		{Term: 0, LeaderID: "B"},
	}
	for _, args := range invalid {
		if reply, err := rf.HandleAppendEntries(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleAppendEntries(%+v) = %+v, %v; want no reply and ErrInvalidArgs", args, reply, err)
		}
	}
	if role, term := roleAndTerm(rf); role != Follower || term != 0 || st.saveCount() != 0 {
		t.Fatalf("invalid request changed state: %v in term %d, %d saves", role, term, st.saveCount())
	}
	if rf.electionTimer.Stop() {
		t.Fatalf("invalid request armed the election timer")
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

func TestRestart_RestoresTermAndVote(t *testing.T) {
	// A restarted node must not vote twice in one term (§5.1, Figure 2).
	rf, st := newTestNode(t, "A", "B", "C")
	if !mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 4, CandidateID: "B"}).VoteGranted {
		t.Fatalf("setup: expected vote for B granted")
	}

	restarted := restartTestNode(t, st, "A", "B", "C")

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

func TestHandleAppendEntries_SecondLeaderInTermGetsNoReply(t *testing.T) {
	// Election Safety is already broken if this happens, but a message from
	// the network must not crash the node. Success=false would read as a log
	// mismatch (§5.3), so there must be no reply at all.
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 3, "A")

	reply, err := rf.HandleAppendEntries(&AppendEntriesArgs{Term: 3, LeaderID: "B"})
	if !errors.Is(err, ErrLeaderConflict) || reply != nil {
		t.Fatalf("expected ErrLeaderConflict and no reply, got %+v, %v", reply, err)
	}
	if role, term := roleAndTerm(rf); role != Leader || term != 3 {
		t.Fatalf("expected to stay leader of term 3, got %v in term %d", role, term)
	}
}

func TestLeaderHint_FollowsAppendEntriesAndTerms(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	if got := rf.Leader(); got != "" {
		t.Fatalf("fresh node knows leader %q, want none", got)
	}

	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B"})
	if got := rf.Leader(); got != "B" {
		t.Fatalf("after B's AppendEntries in term 2: leader %q, want B", got)
	}

	// A deposed leader of an earlier term is not followed.
	if reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 1, LeaderID: "C"}); reply.Success {
		t.Fatalf("stale AppendEntries accepted: %+v", reply)
	}
	if got := rf.Leader(); got != "B" {
		t.Fatalf("after a stale AppendEntries from C: leader %q, want B", got)
	}

	// B led term 2 only; nobody is known to lead term 3 yet.
	mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 3, CandidateID: "C"})
	if got := rf.Leader(); got != "" {
		t.Fatalf("after moving to term 3: leader %q, want none", got)
	}
}

func TestLeaderHint_CandidateLearnsTheWinner(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Candidate, 2, "A")

	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B"})

	if got := rf.Leader(); got != "B" {
		t.Fatalf("candidate that lost to B: leader %q, want B", got)
	}
}
