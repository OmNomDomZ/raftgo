package raft

import (
	"fmt"
	"slices"
)

// EntryKind tells what a log entry carries.
type EntryKind uint8

const (
	// EntryCommand carries a client command for the state machine.
	EntryCommand EntryKind = iota
	// EntryNoOp is appended by every new leader. A leader only commits
	// entries of its own term by counting replicas (§5.4.2), so without it
	// entries from earlier terms would wait for the next client command (§8).
	EntryNoOp
)

// LogEntry is one entry of the replicated log (Figure 2).
type LogEntry struct {
	Term    Term
	Kind    EntryKind
	Command []byte // nil for EntryNoOp
}

// The in-memory log starts with a sentinel entry of term 0 at index 0, so
// index i is rf.log[i] and PrevLogIndex 0 always matches (§5.3). Everything
// else goes through the helpers below, so log compaction only has to change
// them.

func (rf *Raft) lastLogIndexLocked() Index {
	return Index(len(rf.log) - 1)
}

func (rf *Raft) lastLogTermLocked() Term {
	return rf.log[len(rf.log)-1].Term
}

// termAtLocked returns the term of the entry at i, which must be at most
// lastLogIndexLocked.
func (rf *Raft) termAtLocked(i Index) Term {
	return rf.log[i].Term
}

// entriesLocked returns a copy of the entries in [lo, hi). It is a copy
// because the log's backing array is overwritten when a suffix is replaced,
// while the result may still be in flight in an RPC or on its way to
// ApplyCh.
func (rf *Raft) entriesLocked(lo, hi Index) []LogEntry {
	if lo >= hi {
		return nil
	}
	return slices.Clone(rf.log[lo:hi])
}

// firstIndexOfTermLocked returns the first index of the run of entries that
// has the same term as the entry at i (i >= 1).
func (rf *Raft) firstIndexOfTermLocked(i Index) Index {
	term := rf.termAtLocked(i)
	for i > 1 && rf.termAtLocked(i-1) == term {
		i--
	}
	return i
}

// lastIndexOfTermLocked returns the last index of an entry of the given
// term, if the log has one. Terms never decrease along the log.
func (rf *Raft) lastIndexOfTermLocked(term Term) (Index, bool) {
	for i := rf.lastLogIndexLocked(); i > 0; i-- {
		switch t := rf.termAtLocked(i); {
		case t == term:
			return i, true
		case t < term:
			return 0, false
		}
	}
	return 0, false
}

// storeLogLocked makes entries durable in place of the log from index from
// on, and only then adopts them in memory, so a failed write leaves the log
// unchanged: a node must not acknowledge entries a restart would lose.
func (rf *Raft) storeLogLocked(from Index, entries []LogEntry) error {
	if from == 0 || from > rf.lastLogIndexLocked()+1 {
		rf.fatalf("storing entries from index %d, but the log ends at %d", from, rf.lastLogIndexLocked())
	}
	if err := rf.storage.StoreLog(from, entries); err != nil {
		rf.logger.Error("persist log failed", "from", from, "entries", len(entries), "err", err)
		return err
	}
	rf.log = append(rf.log[:from], entries...)
	return nil
}

// checkEntries reports why entries could not follow an entry of term prev in
// a log whose terms are at most maxTerm: terms start at 1 and never decrease
// along a log (§5.3).
func checkEntries(prev Term, entries []LogEntry, maxTerm Term) error {
	for i, e := range entries {
		switch {
		case e.Kind != EntryCommand && e.Kind != EntryNoOp:
			return fmt.Errorf("entry %d has unknown kind %d", i, e.Kind)
		case e.Term == 0:
			return fmt.Errorf("entry %d has term 0", i)
		case e.Term < prev:
			return fmt.Errorf("entry %d has term %d after term %d", i, e.Term, prev)
		case e.Term > maxTerm:
			return fmt.Errorf("entry %d has term %d above term %d", i, e.Term, maxTerm)
		}
		prev = e.Term
	}
	return nil
}
