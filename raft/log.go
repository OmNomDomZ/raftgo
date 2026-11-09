package raft

import (
	"fmt"
	"slices"
)

type EntryKind uint8

const (
	EntryCommand EntryKind = iota
	// No-op нового лидера позволяет подтвердить префикс через запись текущего term.
	EntryNoOp
)

// LogEntry хранит term, вид записи и байты команды.
type LogEntry struct {
	Term    Term
	Kind    EntryKind
	Command []byte // Для no-op команда отсутствует.
}

// На позиции 0 всегда находится служебная запись term 0.

func (rf *Raft) lastLogIndexLocked() Index {
	return Index(len(rf.log) - 1)
}

func (rf *Raft) lastLogTermLocked() Term {
	return rf.log[len(rf.log)-1].Term
}

func (rf *Raft) termAtLocked(i Index) Term {
	return rf.log[i].Term
}

// Копируем участок лога, чтобы замена хвоста не меняла уже отправленные записи.
func (rf *Raft) entriesLocked(lo, hi Index) []LogEntry {
	if lo >= hi {
		return nil
	}
	return slices.Clone(rf.log[lo:hi])
}

// Ищем начало участка с тем же term для ответа о конфликте.
func (rf *Raft) firstIndexOfTermLocked(i Index) Index {
	term := rf.termAtLocked(i)
	for i > 1 && rf.termAtLocked(i-1) == term {
		i--
	}
	return i
}

// Ищем последнюю запись указанного term для возврата к общему префиксу.
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

// Заменяем суффикс с позиции from: сначала в Storage, затем в рабочем логе.
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

// Проверяем вид записей и неубывающий порядок term в допустимых границах.
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
