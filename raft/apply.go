package raft

import "context"

// ApplyMsg передаёт приложению подтверждённую запись, включая no-op.
type ApplyMsg struct {
	Index   Index
	Term    Term
	Kind    EntryKind
	Command []byte // Получатель не должен менять эти байты.
}

// Будим обработчик применения после продвижения commitIndex.
func (rf *Raft) wakeApplierLocked() {
	select {
	case rf.applyWake <- struct{}{}:
	default:
	}
}

// Передаём подтверждённые записи в ApplyCh по порядку, не удерживая mutex.
func (rf *Raft) apply(ctx context.Context) {
	var applied Index
	for {
		select {
		case <-ctx.Done():
			return
		case <-rf.applyWake:
		}

		rf.mu.Lock()
		first := applied + 1
		entries := rf.entriesLocked(first, rf.commitIndex+1)
		rf.mu.Unlock()

		for i, e := range entries {
			msg := ApplyMsg{Index: first + Index(i), Term: e.Term, Kind: e.Kind, Command: e.Command}
			select {
			case rf.applyCh <- msg:
			case <-ctx.Done():
				return
			}
		}
		applied += Index(len(entries))
	}
}
