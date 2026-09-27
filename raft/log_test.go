package raft

import (
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
