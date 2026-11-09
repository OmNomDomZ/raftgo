package raft

import (
	"errors"
	"fmt"
	"math"
)

var _ RPCHandler = (*Raft)(nil)

// ErrStopped: узел остановлен и не отвечает на RPC.
var ErrStopped = errors.New("raft: node is stopped")

// ErrNotStarted: обработка RPC запрещена до запуска Run.
var ErrNotStarted = errors.New("raft: node is not started")

// ErrInvalidArgs: запрос RPC содержит недопустимые данные.
var ErrInvalidArgs = errors.New("raft: invalid RPC arguments")

// ErrLeaderConflict: другой узел объявил себя лидером в нашем term.
var ErrLeaderConflict = errors.New("raft: another leader in this term")

// ErrCommittedConflict: запрос пытается заменить подтверждённую запись.
var ErrCommittedConflict = errors.New("raft: request conflicts with a committed entry")

// Решаем, можно ли выдать голос кандидату, и сохраняем голос перед ответом.
func (rf *Raft) HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error) {
	if err := rf.checkRequestVoteArgs(args); err != nil {
		return nil, err
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if err := rf.runningLocked(); err != nil {
		return nil, err
	}
	if args.Term < rf.currentTerm {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}, nil
	}
	if args.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(args.Term)
	}

	if rf.votedFor != "" && rf.votedFor != args.CandidateID {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}, nil
	}
	// Не голосуем за кандидата с менее свежим логом.
	if !rf.upToDateLocked(args.LastLogIndex, args.LastLogTerm) {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}, nil
	}

	// Повторный голос тому же кандидату не требует записи, если term уже сохранён.
	if rf.votedFor != args.CandidateID || rf.dirty {
		st := rf.persistentStateLocked()
		st.VotedFor = args.CandidateID
		if err := rf.persistLocked(st); err != nil {
			return nil, fmt.Errorf("raft: persist vote for %s in term %d: %w", args.CandidateID, st.CurrentTerm, err)
		}
		rf.logger.Info("voted", "term", rf.currentTerm, "candidate", args.CandidateID)
	}
	rf.resetElectionTimerLocked()
	return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: true}, nil
}

// Проверяем term лидера и передаём записи на проверку лога.
func (rf *Raft) HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	if err := rf.checkAppendEntriesArgs(args); err != nil {
		return nil, err
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if err := rf.runningLocked(); err != nil {
		return nil, err
	}
	if args.Term < rf.currentTerm {
		return &AppendEntriesReply{Term: rf.currentTerm, Success: false}, nil
	}
	if args.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(args.Term)
	} else {
		switch rf.role {
		case Leader:
			// Два лидера одного term — нарушение протокола; не подтверждаем запрос.
			rf.logger.Error("another leader in our term, not answering (Election Safety, §5.2)",
				"term", args.Term, "otherLeader", args.LeaderID)
			return nil, fmt.Errorf("%w: %s in term %d", ErrLeaderConflict, args.LeaderID, args.Term)
		case Candidate:
			rf.logger.Info("stepping down, another node won the election",
				"term", rf.currentTerm, "leader", args.LeaderID)
			rf.role = Follower
			rf.cancelRPCsLocked()
		}
	}
	rf.leaderID = args.LeaderID
	rf.resetElectionTimerLocked()

	// Сохраняем принятый term перед подтверждением лидеру.
	if err := rf.syncLocked(); err != nil {
		return nil, fmt.Errorf("raft: persist term %d: %w", rf.currentTerm, err)
	}

	return rf.appendEntriesLocked(args)
}

// Проверяем основание лога, сохраняем новый суффикс и обновляем commitIndex.
func (rf *Raft) appendEntriesLocked(args *AppendEntriesArgs) (*AppendEntriesReply, error) {
	last := rf.lastLogIndexLocked()
	if args.PrevLogIndex > last {
		return &AppendEntriesReply{Term: rf.currentTerm, ConflictIndex: last + 1}, nil
	}
	if term := rf.termAtLocked(args.PrevLogIndex); term != args.PrevLogTerm {
		return &AppendEntriesReply{
			Term:          rf.currentTerm,
			ConflictTerm:  term,
			ConflictIndex: rf.firstIndexOfTermLocked(args.PrevLogIndex),
		}, nil
	}

	// Пропускаем совпавшие записи, чтобы повторный запрос не усекал корректный хвост.
	skip := 0
	for skip < len(args.Entries) {
		i := args.PrevLogIndex + 1 + Index(skip)
		if i > last || rf.termAtLocked(i) != args.Entries[skip].Term {
			break
		}
		skip++
	}
	if skip < len(args.Entries) {
		from := args.PrevLogIndex + 1 + Index(skip)
		if from <= rf.commitIndex {
			rf.logger.Error("leader overwrites a committed entry, not answering (State Machine Safety, §5.4.3)",
				"term", args.Term, "leader", args.LeaderID, "index", from, "commitIndex", rf.commitIndex)
			return nil, fmt.Errorf("%w: index %d from %s in term %d, commit index %d",
				ErrCommittedConflict, from, args.LeaderID, args.Term, rf.commitIndex)
		}
		// Сначала сохраняем записи в Storage, затем отвечаем об успехе.
		if err := rf.storeLogLocked(from, args.Entries[skip:]); err != nil {
			return nil, fmt.Errorf("raft: persist entries from %d in term %d: %w", from, rf.currentTerm, err)
		}
	}

	// Подтверждаем только ту часть лога, совпадение которой проверено этим запросом.
	if commit := min(args.LeaderCommit, args.PrevLogIndex+Index(len(args.Entries))); commit > rf.commitIndex {
		rf.commitIndex = commit
		rf.wakeApplierLocked()
	}
	return &AppendEntriesReply{Term: rf.currentTerm, Success: true}, nil
}

// Сравниваем term последних записей, затем их индексы.
func (rf *Raft) upToDateLocked(lastIndex Index, lastTerm Term) bool {
	if ours := rf.lastLogTermLocked(); lastTerm != ours {
		return lastTerm > ours
	}
	return lastIndex >= rf.lastLogIndexLocked()
}

func (rf *Raft) runningLocked() error {
	switch {
	case !rf.started:
		return ErrNotStarted
	case rf.stoppedLocked():
		return ErrStopped
	}
	return nil
}

// Проверяем обязательные поля и согласованность term и индекса кандидата.
func (rf *Raft) checkRequestVoteArgs(args *RequestVoteArgs) error {
	switch {
	case args == nil:
		return fmt.Errorf("%w: nil RequestVote", ErrInvalidArgs)
	case args.CandidateID == "":
		return fmt.Errorf("%w: RequestVote without a candidate ID", ErrInvalidArgs)
	case args.CandidateID == rf.id:
		return fmt.Errorf("%w: RequestVote from this node itself", ErrInvalidArgs)
	case args.Term == 0:
		return fmt.Errorf("%w: RequestVote from %s in term 0", ErrInvalidArgs, args.CandidateID)
	case args.LastLogTerm > args.Term:
		return fmt.Errorf("%w: RequestVote from %s in term %d with last log term %d",
			ErrInvalidArgs, args.CandidateID, args.Term, args.LastLogTerm)
	case (args.LastLogIndex == 0) != (args.LastLogTerm == 0):
		return fmt.Errorf("%w: RequestVote from %s with last log entry %d of term %d",
			ErrInvalidArgs, args.CandidateID, args.LastLogIndex, args.LastLogTerm)
	}
	return nil
}

// Проверяем поля AppendEntries и порядок term в переданных записях.
func (rf *Raft) checkAppendEntriesArgs(args *AppendEntriesArgs) error {
	switch {
	case args == nil:
		return fmt.Errorf("%w: nil AppendEntries", ErrInvalidArgs)
	case args.LeaderID == "":
		return fmt.Errorf("%w: AppendEntries without a leader ID", ErrInvalidArgs)
	case args.LeaderID == rf.id:
		return fmt.Errorf("%w: AppendEntries from this node itself", ErrInvalidArgs)
	case args.Term == 0:
		return fmt.Errorf("%w: AppendEntries from %s in term 0", ErrInvalidArgs, args.LeaderID)
	case args.PrevLogTerm > args.Term || (args.PrevLogIndex == 0) != (args.PrevLogTerm == 0):
		return fmt.Errorf("%w: AppendEntries from %s in term %d with prev log entry %d of term %d",
			ErrInvalidArgs, args.LeaderID, args.Term, args.PrevLogIndex, args.PrevLogTerm)
	case uint64(len(args.Entries)) > math.MaxUint64-uint64(args.PrevLogIndex):
		return fmt.Errorf("%w: AppendEntries from %s overflows the log index", ErrInvalidArgs, args.LeaderID)
	}
	if err := checkEntries(args.PrevLogTerm, args.Entries, args.Term); err != nil {
		return fmt.Errorf("%w: AppendEntries from %s: %w", ErrInvalidArgs, args.LeaderID, err)
	}
	return nil
}
