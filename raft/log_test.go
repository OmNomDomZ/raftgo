package raft

import (
	"errors"
	"slices"
	"testing"
)

// cmdEntry is a command entry whose command is its own name.
func cmdEntry(term Term, cmd string) LogEntry {
	return LogEntry{Term: term, Kind: EntryCommand, Command: []byte(cmd)}
}

// entriesOfTerms returns one command entry per term.
func entriesOfTerms(terms ...Term) []LogEntry {
	entries := make([]LogEntry, len(terms))
	for i, term := range terms {
		entries[i] = cmdEntry(term, "")
	}
	return entries
}

// setLog replaces rf's log, in storage and in memory, with entries.
func setLog(t *testing.T, rf *Raft, entries ...LogEntry) {
	t.Helper()
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.log = rf.log[:1]
	if err := rf.storeLogLocked(1, entries); err != nil {
		t.Fatalf("setLog: %v", err)
	}
}

// logTerms returns the terms of rf's in-memory log, without the sentinel.
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

func commitIndex(rf *Raft) Index {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.commitIndex
}

// --- RequestVote: election restriction (§5.4.1) ---

func TestHandleRequestVote_RequiresUpToDateLog(t *testing.T) {
	// Our log ends with an entry of term 2 at index 3.
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
			// The candidate's term is adopted either way (§5.1).
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
	// A candidate that cannot win must not hold off our own election, or a
	// node with a stale log could keep the cluster leaderless.
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
		{Term: 2, CandidateID: "B", LastLogIndex: 1, LastLogTerm: 3}, // log term above own term
		{Term: 2, CandidateID: "B", LastLogIndex: 1, LastLogTerm: 0}, // real entry of term 0
		{Term: 2, CandidateID: "B", LastLogIndex: 0, LastLogTerm: 1}, // sentinel with a term
	} {
		if reply, err := rf.HandleRequestVote(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleRequestVote(%+v) = %+v, %v; want ErrInvalidArgs", args, reply, err)
		}
	}
}

// --- AppendEntries receiver (Figure 2, §5.3) ---

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
	// A delayed AppendEntries carries a prefix of what the follower already
	// has. Truncating after it would drop entries the leader may have
	// counted towards a commit.
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
	// Entries past the request are not known to match the leader's log, so
	// the leader's commit index must not reach them (Figure 2).
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 2, "")
	setLog(t, rf, entriesOfTerms(1, 1, 1, 1)...)

	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{
		Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 1, Entries: entriesOfTerms(1), LeaderCommit: 10,
	})
	if got := commitIndex(rf); got != 2 {
		t.Fatalf("commit index %d, want 2", got)
	}

	// An older request never moves the commit index back.
	mustHandleAppendEntries(t, rf, &AppendEntriesArgs{Term: 2, LeaderID: "B", LeaderCommit: 1})
	if got := commitIndex(rf); got != 2 {
		t.Fatalf("commit index moved back to %d", got)
	}
}

func TestHandleAppendEntries_StorageFailureGivesNoReply(t *testing.T) {
	// Success would tell the leader the entries are durable.
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
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 3},                             // prev term above own term
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 0},                             // real entry of term 0
		{Term: 2, LeaderID: "B", PrevLogIndex: 0, PrevLogTerm: 1},                             // sentinel with a term
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(3)},                                  // entry above own term
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(2, 1)},                               // decreasing terms
		{Term: 2, LeaderID: "B", PrevLogIndex: 1, PrevLogTerm: 2, Entries: entriesOfTerms(1)}, // below prev term
		{Term: 2, LeaderID: "B", Entries: entriesOfTerms(0)},                                  // entry of term 0
		{Term: 2, LeaderID: "B", Entries: []LogEntry{{Term: 1, Kind: 7}}},                     // unknown kind
	} {
		if reply, err := rf.HandleAppendEntries(args); !errors.Is(err, ErrInvalidArgs) || reply != nil {
			t.Fatalf("HandleAppendEntries(%+v) = %+v, %v; want ErrInvalidArgs", args, reply, err)
		}
	}
}

// --- Restart ---

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
