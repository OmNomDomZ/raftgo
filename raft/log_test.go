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
