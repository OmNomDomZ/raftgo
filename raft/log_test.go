package raft

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

func cmdEntry(term Term, cmd string) LogEntry {
	return LogEntry{Term: term, Kind: EntryCommand, Command: []byte(cmd)}
}

func entriesOfTerms(terms ...Term) []LogEntry {
	entries := make([]LogEntry, len(terms))
	for i, term := range terms {
		entries[i] = cmdEntry(term, "")
	}
	return entries
}

func setLog(t *testing.T, rf *Raft, entries ...LogEntry) {
	t.Helper()
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.log = rf.log[:1]
	if err := rf.storeLogLocked(1, entries); err != nil {
		t.Fatalf("setLog: %v", err)
	}
}

func logTerms(rf *Raft) []Term {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	var terms []Term
	for _, e := range rf.log[1:] {
		terms = append(terms, e.Term)
	}
	return terms
}

func termsOf(entries []LogEntry) []Term {
	var terms []Term
	for _, e := range entries {
		terms = append(terms, e.Term)
	}
	return terms
}

func entriesEqual(a, b LogEntry) bool {
	return a.Term == b.Term && a.Kind == b.Kind && bytes.Equal(a.Command, b.Command)
}

func commitIndex(rf *Raft) Index {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.commitIndex
}

func TestHandleRequestVote_RequiresUpToDateLog(t *testing.T) {
	tests := []struct {
		name      string
		lastIndex Index
		lastTerm  Term
		grant     bool
	}{
		{"older last term, longer log", 5, 1, false},
		{"same last term, shorter log", 2, 2, false},
		{"same last term, same length", 3, 2, true},
		{"same last term, longer log", 4, 2, true},
		{"newer last term, shorter log", 1, 3, true},
		{"empty log", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf, _ := newTestNode(t, "A", "B", "C")
			setRoleAndTerm(rf, Follower, 3, "")
			setLog(t, rf, entriesOfTerms(1, 1, 2)...)

			reply := mustHandleRequestVote(t, rf, &RequestVoteArgs{
				Term: 4, CandidateID: "B", LastLogIndex: tt.lastIndex, LastLogTerm: tt.lastTerm,
			})
			if reply.VoteGranted != tt.grant {
				t.Fatalf("expected VoteGranted=%v, got %+v", tt.grant, reply)
			}
			if _, term := roleAndTerm(rf); term != 4 || reply.Term != 4 {
				t.Fatalf("expected term 4 adopted, got node term %d, reply term %d", term, reply.Term)
			}
			if want := NodeID(""); !tt.grant && votedFor(rf) != want {
				t.Fatalf("denied candidate recorded as vote: %q", votedFor(rf))
			}
		})
	}
}

func TestHandleRequestVote_DenialForStaleLogDoesNotResetTimer(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1)...)
	setRoleAndTerm(rf, Follower, 1, "")
	rf.electionTimer.Stop()

	mustHandleRequestVote(t, rf, &RequestVoteArgs{Term: 2, CandidateID: "B"})
	if rf.electionTimer.Stop() {
		t.Fatalf("denied vote reset the election timer")
	}
}

func TestHandleRequestVote_RejectsInvalidLastLogEntry(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	for _, args := range []*RequestVoteArgs{
		{Term: 2, CandidateID: "B", LastLogIndex: 1, LastLogTerm: 3},
		{Term: 2, CandidateID: "B", LastLogIndex: 1, LastLogTerm: 0},
		{Term: 2, CandidateID: "B", LastLogIndex: 0, LastLogTerm: 1},
	} {
		if reply, err := rf.HandleRequestVote(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleRequestVote(%+v) = %+v, %v; want ErrInvalidArgs", args, reply, err)
		}
	}
}

func TestHandleAppendEntries_PrevEntryMissing(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1, 1)...)

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 5, PrevLogTerm: 2, Entries: entriesOfTerms(2),
	})
	if reply.Success || reply.ConflictTerm != 0 || reply.ConflictIndex != 3 {
		t.Fatalf("expected rejection with ConflictIndex 3 and no ConflictTerm, got %+v", reply)
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 1}) {
		t.Fatalf("rejected request changed the log: %v", got)
	}
}

func TestHandleAppendEntries_PrevEntryTermMismatch(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1, 2, 2, 2)...)

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 3, LeaderID: "B", PrevLogIndex: 4, PrevLogTerm: 3,
	})
	if reply.Success || reply.ConflictTerm != 2 || reply.ConflictIndex != 2 {
		t.Fatalf("expected rejection with ConflictTerm 2 from index 2, got %+v", reply)
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 2, 2, 2}) {
		t.Fatalf("rejected request changed the log: %v", got)
	}
}

func TestHandleAppendEntries_AppendsAndPersistsBeforeReplying(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1)...)

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(1, 2),
	})
	if !reply.Success {
		t.Fatalf("expected success, got %+v", reply)
	}
	want := []Term{1, 1, 2}
	if got := logTerms(rf); !slices.Equal(got, want) {
		t.Fatalf("log %v, want %v", got, want)
	}
	if got := termsOf(st.storedLog()); !slices.Equal(got, want) {
		t.Fatalf("stored log %v, want %v", got, want)
	}
}

func TestHandleAppendEntries_TruncatesConflictingSuffix(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1, 2, 2, 2)...)

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 3, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(2, 3),
	})
	if !reply.Success {
		t.Fatalf("expected success, got %+v", reply)
	}
	want := []Term{1, 2, 3}
	if got := logTerms(rf); !slices.Equal(got, want) {
		t.Fatalf("log %v, want %v", got, want)
	}
	if got := termsOf(st.storedLog()); !slices.Equal(got, want) {
		t.Fatalf("stored log %v, want %v", got, want)
	}
}

func TestHandleAppendEntries_StaleRequestDoesNotTruncate(t *testing.T) {
	// Задержанный короткий запрос не должен усекать ранее принятый хвост.
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	setLog(t, rf, entriesOfTerms(1, 2, 2, 2)...)
	writes := st.logWriteCount()

	reply := mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(2),
	})
	if !reply.Success {
		t.Fatalf("expected success, got %+v", reply)
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 2, 2, 2}) {
		t.Fatalf("stale request truncated the log to %v", got)
	}
	if st.logWriteCount() != writes {
		t.Fatalf("request with nothing new wrote the log")
	}
}

func TestHandleAppendEntries_CommitIndexCoversOnlyVerifiedEntries(t *testing.T) {
	// Commit не должен выходить за участок лога, проверенный запросом.
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	setLog(t, rf, entriesOfTerms(1, 1, 1, 1)...)

	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(1), LeaderCommit: 10,
	})
	if got := commitIndex(rf); got != 2 {
		t.Fatalf("commit index %d, want 2", got)
	}

	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B", LeaderCommit: 1})
	if got := commitIndex(rf); got != 2 {
		t.Fatalf("commit index moved back to %d", got)
	}
}

func TestHandleAppendEntries_StorageFailureGivesNoReply(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	setLog(t, rf, entriesOfTerms(1, 1)...)
	st.setFailSave(true)

	args := &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(2), LeaderCommit: 2,
	}
	if reply, err := rf.HandleAppendEntries(args); err == nil {
		t.Fatalf("expected no reply while storage fails, got %+v", reply)
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 1}) {
		t.Fatalf("failed write changed the in-memory log to %v", got)
	}
	if got := commitIndex(rf); got != 0 {
		t.Fatalf("failed write advanced the commit index to %d", got)
	}
}

func TestHandleAppendEntries_RefusesToOverwriteCommittedEntry(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 3, "")
	setLog(t, rf, entriesOfTerms(1, 2)...)
	rf.mu.Lock()
	rf.commitIndex = 2
	rf.mu.Unlock()

	_, err := rf.HandleAppendEntries(&AppendEntriesArgs{
		Term: 3, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(3),
	})
	if !errors.Is(err, ErrCommittedConflict) {
		t.Fatalf("expected ErrCommittedConflict, got %v", err)
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 2}) {
		t.Fatalf("committed entry overwritten: %v", got)
	}
}

func TestHandleAppendEntries_RejectsInvalidLog(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")
	for _, args := range []*AppendEntriesArgs{
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 3},
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 0},
		{Term: 2, LeaderID: "B", PrevLogIndex: 0, PrevLogTerm: 1},
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(3)},
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(2, 1)},
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 2, Entries: entriesOfTerms(1)},
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(0)},
		{Term: 2, LeaderID: "B", Entries: []LogEntry{{Term: 1, Kind: 7}}},
	} {
		if reply, err := rf.HandleAppendEntries(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleAppendEntries(%+v) = %+v, %v; want ErrInvalidArgs", args, reply, err)
		}
	}
}

func TestStart_OnlyOnLeader(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	if _, _, err := rf.Start([]byte("x")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader from a follower, got %v", err)
	}
}

func TestStart_PersistsBeforeReturning(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 2, "A")
	setLog(t, rf, entriesOfTerms(1)...)

	index, term, err := rf.Start([]byte("x"))
	if err != nil || index != 2 || term != 2 {
		t.Fatalf("Start = %d, %d, %v; want 2, 2, nil", index, term, err)
	}
	if got := st.storedLog(); len(got) != 2 || !entriesEqual(got[1], cmdEntry(2, "x")) {
		t.Fatalf("entry not stored: %+v", got)
	}

	st.setFailSave(true)
	if _, _, err := rf.Start([]byte("y")); err == nil {
		t.Fatalf("expected Start to fail while storage fails")
	}
	if got := logTerms(rf); !slices.Equal(got, []Term{1, 2}) {
		t.Fatalf("failed Start changed the log to %v", got)
	}
}

func TestBecomeLeader_AppendsNoOp(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setLog(t, rf, entriesOfTerms(1)...)
	setRoleAndTerm(rf, Candidate, 2, "A")

	rf.mu.Lock()
	rf.becomeLeaderLocked()
	rf.mu.Unlock()

	if _, isLeader := rf.GetState(); !isLeader {
		t.Fatalf("expected leader")
	}
	if got := st.storedLog(); len(got) != 2 || got[1].Kind != EntryNoOp || got[1].Term != 2 {
		t.Fatalf("expected a stored no-op of term 2 at index 2, got %+v", got)
	}
}

func TestBecomeLeader_NoOpPersistFailureStaysCandidate(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Candidate, 2, "A")
	st.setFailSave(true)

	rf.mu.Lock()
	rf.becomeLeaderLocked()
	rf.mu.Unlock()

	if role, _ := roleAndTerm(rf); role != Candidate {
		t.Fatalf("expected to stay Candidate, got %v", role)
	}
	if got := logTerms(rf); len(got) != 0 {
		t.Fatalf("no-op kept in memory despite failed write: %v", got)
	}
}

func TestAdvanceCommit_OnlyCountsReplicasOfCurrentTerm(t *testing.T) {
	// Запись старого term подтверждается только вместе с записью текущего term.
	rf, _ := newTestNode(t, "A", "B", "C", "D", "E")
	setRoleAndTerm(rf, Leader, 4, "A")
	setLog(t, rf, cmdEntry(1, "a"), cmdEntry(2, "b"), cmdEntry(4, "c"))

	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.matchIndex["B"], rf.matchIndex["C"] = 2, 2
	rf.advanceCommitLocked()
	if rf.commitIndex != 0 {
		t.Fatalf("entry of term 2 committed by counting replicas: commit index %d", rf.commitIndex)
	}

	rf.matchIndex["B"] = 3
	rf.advanceCommitLocked()
	if rf.commitIndex != 0 {
		t.Fatalf("committed with only 2 of 5 replicas: commit index %d", rf.commitIndex)
	}

	rf.matchIndex["C"] = 3
	rf.advanceCommitLocked()
	if rf.commitIndex != 3 {
		t.Fatalf("expected index 3 committed on a majority, got commit index %d", rf.commitIndex)
	}
}

type replyingPeer struct {
	reply func(*AppendEntriesArgs) *AppendEntriesReply
}

func (replyingPeer) HandleRequestVote(*RequestVoteArgs) (*RequestVoteReply, error) {
	return nil, errors.New("not implemented")
}

func (p replyingPeer) HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	return p.reply(args), nil
}

func TestSendAppendEntries_BacksOffOverConflictingTerm(t *testing.T) {
	leaderLog := entriesOfTerms(1, 1, 1, 4, 4, 5, 5, 6, 6, 6)
	tests := []struct {
		name  string
		reply AppendEntriesReply
		next  Index
	}{
		{"follower log shorter", AppendEntriesReply{Term: 6, ConflictIndex: 4}, 4},
		{"term leader lacks", AppendEntriesReply{Term: 6, ConflictTerm: 2, ConflictIndex: 2}, 2},
		{"term leader has", AppendEntriesReply{Term: 6, ConflictTerm: 5, ConflictIndex: 6}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf, _ := newTestNode(t, "A", "B", "C")
			setRoleAndTerm(rf, Leader, 6, "A")
			setLog(t, rf, leaderLog...)
			rf.transport.(*fakeTransport).register("B", replyingPeer{func(*AppendEntriesArgs) *AppendEntriesReply {
				reply := tt.reply
				return &reply
			}})

			rf.mu.Lock()
			rf.nextIndex["B"] = 10
			args := rf.appendEntriesArgsLocked("B")
			rf.mu.Unlock()

			if again := rf.sendAppendEntries(context.Background(), "B", args); !again {
				t.Fatalf("expected an immediate retry after a mismatch")
			}
			rf.mu.Lock()
			defer rf.mu.Unlock()
			if got := rf.nextIndex["B"]; got != tt.next {
				t.Fatalf("nextIndex %d, want %d", got, tt.next)
			}
		})
	}
}

func TestSendAppendEntries_SuccessAdvancesMatchAndCommit(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 2, "A")
	setLog(t, rf, entriesOfTerms(2, 2, 2)...)
	rf.transport.(*fakeTransport).register("B", replyingPeer{func(args *AppendEntriesArgs) *AppendEntriesReply {
		return &AppendEntriesReply{Term: args.Term, Success: true}
	}})

	rf.mu.Lock()
	rf.nextIndex["B"] = 1
	rf.maxEntriesPerRPC = 2
	args := rf.appendEntriesArgsLocked("B")
	rf.mu.Unlock()
	if len(args.Entries) != 2 {
		t.Fatalf("expected MaxEntriesPerRPC to cap the batch at 2, got %d", len(args.Entries))
	}

	if again := rf.sendAppendEntries(context.Background(), "B", args); !again {
		t.Fatalf("expected an immediate follow-up while B is behind")
	}
	rf.mu.Lock()
	match, next, commit := rf.matchIndex["B"], rf.nextIndex["B"], rf.commitIndex
	rf.mu.Unlock()
	if match != 2 || next != 3 || commit != 2 {
		t.Fatalf("matchIndex %d nextIndex %d commitIndex %d, want 2 3 2", match, next, commit)
	}
}

func TestSendAppendEntries_ReplyForEarlierTermIsIgnored(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Leader, 3, "A")
	setLog(t, rf, entriesOfTerms(2, 3)...)
	rf.transport.(*fakeTransport).register("B", grantingPeer{})

	rf.sendAppendEntries(context.Background(), "B", &AppendEntriesArgs{
		Term: 2, LeaderID: "A", PrevLogIndex: 0, Entries: entriesOfTerms(2, 3)[:1],
	})
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.matchIndex["B"] != 0 {
		t.Fatalf("success for a term-2 request counted in term 3: matchIndex %d", rf.matchIndex["B"])
	}
}

func TestSendAppendEntries_BrokenPeerLoggedOncePerTerm(t *testing.T) {
	var logs bytes.Buffer
	cfg := testConfig(&fakeStorage{}, "A", "B", "C")
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	markRunning(rf)
	setLog(t, rf, entriesOfTerms(2, 2, 2)...)
	setRoleAndTerm(rf, Leader, 2, "A")
	// Сосед не должен отвергать префикс, который уже подтвердил в этом term.
	rf.transport.(*fakeTransport).register("B", replyingPeer{func(args *AppendEntriesArgs) *AppendEntriesReply {
		return &AppendEntriesReply{Term: args.Term, ConflictIndex: 1}
	}})
	rf.mu.Lock()
	rf.matchIndex["B"], rf.nextIndex["B"] = 2, 3
	rf.mu.Unlock()

	for range 3 {
		rf.mu.Lock()
		args := rf.appendEntriesArgsLocked("B")
		rf.mu.Unlock()
		rf.sendAppendEntries(context.Background(), "B", args)
	}
	if n := strings.Count(logs.String(), "rejected a prefix"); n != 1 {
		t.Fatalf("broken peer logged %d times, want once: %s", n, logs.String())
	}
	rf.mu.Lock()
	next := rf.nextIndex["B"]
	rf.mu.Unlock()
	if next != 3 {
		t.Fatalf("nextIndex backed off below matchIndex to %d", next)
	}
}

func TestAppendEntriesReply_InvalidConflictIsAFailedRPC(t *testing.T) {
	args := &AppendEntriesArgs{Term: 2, LeaderID: "A", PrevLogIndex: 3, PrevLogTerm: 1}
	for _, reply := range []*AppendEntriesReply{
		{Term: 2, ConflictIndex: 0},
		{Term: 2, ConflictIndex: 4},
	} {
		if err := checkAppendEntriesReply(args, reply); !errors.Is(err, errInvalidReply) {
			t.Fatalf("checkAppendEntriesReply(%+v) = %v, want errInvalidReply", reply, err)
		}
	}
	if err := checkAppendEntriesReply(args, &AppendEntriesReply{Term: 3}); err != nil {
		t.Fatalf("rejection from a later term: %v", err)
	}
}

func TestNew_RestoresLog(t *testing.T) {
	rf, st := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	if err := st.SaveState(PersistentState{CurrentTerm: 2}); err != nil {
		t.Fatal(err)
	}
	setLog(t, rf, entriesOfTerms(1, 2)...)

	restarted := restartTestNode(t, st, "A", "B", "C")
	if got := logTerms(restarted); !slices.Equal(got, []Term{1, 2}) {
		t.Fatalf("restored log %v, want [1 2]", got)
	}
	if got := commitIndex(restarted); got != 0 {
		t.Fatalf("commit index is volatile, got %d after restart", got)
	}
}

func TestNew_RejectsCorruptLog(t *testing.T) {
	for name, log := range map[string][]LogEntry{
		"term above current term": entriesOfTerms(1, 3),
		"decreasing terms":        entriesOfTerms(2, 1),
		"term 0":                  entriesOfTerms(0),
		"unknown kind":            {{Term: 1, Kind: 7}},
	} {
		t.Run(name, func(t *testing.T) {
			st := &fakeStorage{state: PersistentState{CurrentTerm: 2}, log: log}
			if _, err := New(testConfig(st, "A", "B")); err == nil {
				t.Fatalf("expected New to reject the stored log")
			}
		})
	}
}

func TestNew_LogLoadFailureIsReported(t *testing.T) {
	if _, err := New(testConfig(&fakeStorage{failLoadLog: true}, "A", "B")); err == nil {
		t.Fatalf("expected New to fail when the log cannot be loaded")
	}
}

func TestApply_SendsEveryCommittedEntryInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		applyCh := make(chan ApplyMsg)
		cfg := testConfig(&fakeStorage{}, "A")
		cfg.ApplyCh = applyCh
		rf, err := New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, stop := runNode(rf)
		defer stop()

		for {
			if _, isLeader := rf.GetState(); isLeader {
				break
			}
			synctest.Wait()
			rf.clock.(*fakeClock).Advance(rf.electionTimeout())
		}
		for _, cmd := range []string{"a", "b"} {
			if _, _, err := rf.Start([]byte(cmd)); err != nil {
				t.Fatalf("Start: %v", err)
			}
		}

		// No-op тоже передаётся в ApplyCh, чтобы не было пропуска индекса.
		want := []ApplyMsg{
			{Index: 1, Term: 1, Kind: EntryNoOp},
			{Index: 2, Term: 1, Kind: EntryCommand, Command: []byte("a")},
			{Index: 3, Term: 1, Kind: EntryCommand, Command: []byte("b")},
		}
		for _, w := range want {
			msg := <-applyCh
			if msg.Index != w.Index || msg.Term != w.Term || msg.Kind != w.Kind || !bytes.Equal(msg.Command, w.Command) {
				t.Fatalf("got %+v, want %+v", msg, w)
			}
		}
	})
}
