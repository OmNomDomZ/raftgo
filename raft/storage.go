package raft

// Storage атомарно сохраняет term, голос и лог; при допустимых отказах данные не теряются.
type Storage interface {
	SaveState(state PersistentState) error
	// Пустое состояние допустимо только для нового узла, не при ошибке чтения.
	LoadState() (PersistentState, error)

	// StoreLog атомарно заменяет суффикс с позиции from, начиная с индекса 1.
	StoreLog(from Index, entries []LogEntry) error
	// LoadLog возвращает независимую копию лога без служебной позиции 0.
	LoadLog() ([]LogEntry, error)
}

// PersistentState хранит term и голос; записи лога сохраняются отдельно.
type PersistentState struct {
	CurrentTerm Term
	VotedFor    NodeID // Пустая строка — голос в текущем term ещё не отдан.
}

func (rf *Raft) persistentStateLocked() PersistentState {
	return PersistentState{CurrentTerm: rf.currentTerm, VotedFor: rf.votedFor}
}

// Сначала сохраняем term и голос в Storage, затем обновляем поля узла.
func (rf *Raft) persistLocked(st PersistentState) error {
	if err := rf.storage.SaveState(st); err != nil {
		rf.logger.Error("persist state failed",
			"term", st.CurrentTerm, "votedFor", st.VotedFor, "err", err)
		return err
	}
	rf.currentTerm = st.CurrentTerm
	rf.votedFor = st.VotedFor
	rf.dirty = false
	return nil
}

// Сохраняем term и голос, если они изменились после перехода на больший term.
func (rf *Raft) syncLocked() error {
	if !rf.dirty {
		return nil
	}
	return rf.persistLocked(rf.persistentStateLocked())
}
