package raft

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrNotLeader: узел не лидер и не принимает предложение.
var ErrNotLeader = errors.New("raft: not the leader")

// NotLeaderError передаёт клиенту ID известного лидера, если он есть.
type NotLeaderError struct {
	Leader NodeID
}

func (e *NotLeaderError) Error() string {
	if e.Leader == "" {
		return ErrNotLeader.Error() + ", leader unknown"
	}
	return fmt.Sprintf("%v, leader is %s", ErrNotLeader, e.Leader)
}

func (e *NotLeaderError) Unwrap() error { return ErrNotLeader }

func (rf *Raft) notLeaderErrLocked() error {
	return &NotLeaderError{Leader: rf.leaderLocked()}
}

// Start сохраняет предложение лидера; cmd менять нельзя, подтверждение index/term приходит через ApplyCh.
func (rf *Raft) Start(cmd []byte) (Index, Term, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if !rf.isLeaderLocked() {
		return 0, rf.currentTerm, rf.notLeaderErrLocked()
	}
	index := rf.lastLogIndexLocked() + 1
	entry := LogEntry{Term: rf.currentTerm, Kind: EntryCommand, Command: cmd}
	if err := rf.storeLogLocked(index, []LogEntry{entry}); err != nil {
		return 0, rf.currentTerm, fmt.Errorf("raft: persist entry %d: %w", index, err)
	}
	rf.advanceCommitLocked()
	rf.wakeReplicatorsLocked()
	return index, rf.currentTerm, nil
}

func (rf *Raft) onHeartbeatTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() || rf.role != Leader {
		return
	}
	rf.heartbeatTimer.Reset(rf.heartbeatInterval)
	rf.wakeReplicatorsLocked()
}

func (rf *Raft) wakeReplicatorsLocked() {
	for _, p := range rf.peers {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// По сигналу отправляем записи одному соседу; одновременно выполняется только один его RPC.
func (rf *Raft) replicate(ctx context.Context, p peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}

		for {
			rf.mu.Lock()
			if rf.stoppedLocked() || rf.role != Leader {
				rf.mu.Unlock()
				break
			}
			rpcCtx := rf.rpcContextLocked(ctx)
			args := rf.appendEntriesArgsLocked(p.id)
			rf.mu.Unlock()

			if !rf.sendAppendEntries(rpcCtx, p.id, args) {
				break
			}
		}
	}
}

// Готовим следующую порцию AppendEntries начиная с nextIndex соседа.
func (rf *Raft) appendEntriesArgsLocked(peer NodeID) *AppendEntriesArgs {
	next := rf.nextIndex[peer]
	end := min(rf.lastLogIndexLocked()+1, next+Index(rf.maxEntriesPerRPC))
	return &AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderID:     rf.id,
		PrevLogIndex: next - 1,
		PrevLogTerm:  rf.termAtLocked(next - 1),
		Entries:      rf.entriesLocked(next, end),
		LeaderCommit: rf.commitIndex,
	}
}

// Отправляем AppendEntries и возвращаем true, если нужна следующая попытка без ожидания таймера.
func (rf *Raft) sendAppendEntries(ctx context.Context, peer NodeID, args *AppendEntriesArgs) (again bool) {
	ctx, cancel := rf.withTimeout(ctx, rf.appendEntriesTimeout)
	defer cancel()
	reply, err := rf.transport.AppendEntries(ctx, peer, args)
	if err == nil {
		err = checkAppendEntriesReply(args, reply)
	}
	if err != nil {
		rf.logRPCFailure("AppendEntries", peer, args.Term, err)
		return false
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return false
	}
	if reply.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(reply.Term)
		return false
	}
	if rf.role != Leader || rf.currentTerm != args.Term {
		return false
	}
	// Ответ нашего term учитывается при проверке кворума даже при конфликте лога.
	rf.heardFrom[peer] = struct{}{}

	if reply.Success {
		if match := args.PrevLogIndex + Index(len(args.Entries)); match > rf.matchIndex[peer] {
			rf.matchIndex[peer] = match
			rf.advanceCommitLocked()
		}
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1
		return rf.nextIndex[peer] <= rf.lastLogIndexLocked()
	}

	if args.PrevLogIndex <= rf.matchIndex[peer] {
		// Отказ от ранее подтверждённого префикса означает нарушение протокола.
		if _, logged := rf.mismatchLogged[peer]; !logged {
			rf.mismatchLogged[peer] = struct{}{}
			rf.logger.Error("peer rejected a prefix it had matched (Log Matching, §5.3)",
				"peer", peer, "term", args.Term, "prevLogIndex", args.PrevLogIndex, "matchIndex", rf.matchIndex[peer])
		}
		return false
	}
	// Подсказка о конфликте позволяет пропустить сразу весь участок одного term.
	next := reply.ConflictIndex
	if reply.ConflictTerm != 0 {
		if last, ok := rf.lastIndexOfTermLocked(reply.ConflictTerm); ok {
			next = last + 1
		}
	}
	// Отступаем хотя бы на одну позицию, но не ниже уже подтверждённого префикса.
	rf.nextIndex[peer] = max(min(next, args.PrevLogIndex), rf.matchIndex[peer]+1)
	return true
}

// Продвигаем commitIndex по большинству только через запись текущего term.
func (rf *Raft) advanceCommitLocked() {
	matched := []Index{rf.lastLogIndexLocked()}
	for _, p := range rf.peers {
		matched = append(matched, rf.matchIndex[p.id])
	}
	slices.Sort(matched)
	n := matched[len(matched)-rf.quorum()]
	if n <= rf.commitIndex || rf.termAtLocked(n) != rf.currentTerm {
		return
	}
	rf.commitIndex = n
	rf.wakeApplierLocked()
}

// Проверяем term ответа и корректность подсказки о конфликте.
func checkAppendEntriesReply(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	switch {
	case reply == nil:
		return errNilReply
	case reply.Term < args.Term:
		return fmt.Errorf("%w: AppendEntries reply term %d below request term %d", errInvalidReply, reply.Term, args.Term)
	case reply.Success && reply.Term != args.Term:
		return fmt.Errorf("%w: success in term %d for request term %d", errInvalidReply, reply.Term, args.Term)
	case !reply.Success && reply.Term == args.Term &&
		(reply.ConflictIndex == 0 || reply.ConflictIndex > args.PrevLogIndex):
		return fmt.Errorf("%w: log mismatch at conflict index %d for prev log index %d",
			errInvalidReply, reply.ConflictIndex, args.PrevLogIndex)
	}
	return nil
}
