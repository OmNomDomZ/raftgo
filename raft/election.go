package raft

import (
	"context"
	"fmt"
)

func (rf *Raft) onElectionTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return
	}
	// Игнорируем старый сигнал, если таймер уже перевзведён.
	if rf.clock.Now().Before(rf.electionDeadline) {
		return
	}
	if rf.role == Leader {
		rf.checkQuorumLocked()
		return
	}
	rf.startElectionLocked(ctx)
}

// Начинаем новый term, голосуем за себя и запрашиваем голоса соседей.
func (rf *Raft) startElectionLocked(ctx context.Context) {
	// Следующая попытка выборов нужна и при ошибке сохранения.
	rf.resetElectionTimerLocked()

	st := rf.persistentStateLocked()
	st.CurrentTerm++
	st.VotedFor = rf.id
	if err := rf.persistLocked(st); err != nil {
		return
	}
	rf.logger.Info("became candidate", "term", rf.currentTerm, "oldRole", rf.role)
	rf.role = Candidate
	rf.leaderID = ""
	rf.votes = map[NodeID]struct{}{rf.id: {}}
	rf.cancelRPCsLocked()

	if len(rf.votes) >= rf.quorum() {
		rf.becomeLeaderLocked()
		return
	}

	ctx = rf.rpcContextLocked(ctx)
	args := &RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateID:  rf.id,
		LastLogIndex: rf.lastLogIndexLocked(),
		LastLogTerm:  rf.lastLogTermLocked(),
	}
	for _, p := range rf.peers {
		rf.wg.Go(func() { rf.requestVote(ctx, p.id, args) })
	}
}

func (rf *Raft) requestVote(ctx context.Context, peer NodeID, args *RequestVoteArgs) {
	ctx, cancel := rf.withTimeout(ctx, rf.requestVoteTimeout)
	defer cancel()
	reply, err := rf.transport.RequestVote(ctx, peer, args)
	if err == nil {
		err = checkVoteReply(args, reply)
	}
	if err != nil {
		rf.logRPCFailure("RequestVote", peer, args.Term, err)
		return
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return
	}
	if reply.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(reply.Term)
		return
	}
	if rf.role != Candidate || rf.currentTerm != args.Term || !reply.VoteGranted {
		return
	}

	rf.votes[peer] = struct{}{}
	if len(rf.votes) >= rf.quorum() {
		rf.becomeLeaderLocked()
	}
}

// Проверяем, что ответ относится к допустимому term голосования.
func checkVoteReply(args *RequestVoteArgs, reply *RequestVoteReply) error {
	switch {
	case reply == nil:
		return errNilReply
	case reply.Term < args.Term:
		return fmt.Errorf("%w: RequestVote reply term %d below request term %d", errInvalidReply, reply.Term, args.Term)
	case reply.VoteGranted && reply.Term != args.Term:
		return fmt.Errorf("%w: vote granted in term %d for request term %d", errInvalidReply, reply.Term, args.Term)
	}
	return nil
}
