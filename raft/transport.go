package raft

import "context"

// Transport отправляет RPC соседям; переданные запросы и ответы изменять нельзя.
type Transport interface {
	RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error)
	AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RPCHandler обрабатывает входящие RPC; ошибка означает, что ответ не выдан.
type RPCHandler interface {
	HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error)
	HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RequestVoteArgs содержит term кандидата и положение последней записи его лога.
type RequestVoteArgs struct {
	Term         Term
	CandidateID  NodeID
	LastLogIndex Index
	LastLogTerm  Term
}

type RequestVoteReply struct {
	Term        Term
	VoteGranted bool
}

// AppendEntriesArgs передаёт записи после указанной пары index/term и индекс коммита лидера.
type AppendEntriesArgs struct {
	Term         Term
	LeaderID     NodeID
	PrevLogIndex Index
	PrevLogTerm  Term
	Entries      []LogEntry // Пустой список — heartbeat.
	LeaderCommit Index
}

// AppendEntriesReply подтверждает запись или указывает место конфликта лога.
type AppendEntriesReply struct {
	Term          Term
	Success       bool
	ConflictTerm  Term
	ConflictIndex Index
}
