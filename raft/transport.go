package raft

import "context"

// Transport delivers outgoing RPCs to peers. Implementations must be safe for
// concurrent use and must return promptly once ctx is done. A nil error means
// the reply is non-nil.
type Transport interface {
	RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error)
	AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RPCHandler is a node's inbound RPC surface; *Raft implements it.
type RPCHandler interface {
	HandleRequestVote(args *RequestVoteArgs) *RequestVoteReply
	HandleAppendEntries(args *AppendEntriesArgs) *AppendEntriesReply
}

// RequestVoteArgs is the RequestVote RPC request (Figure 2).
type RequestVoteArgs struct {
	Term         Term
	CandidateID  NodeID
	LastLogIndex LogIndex
	LastLogTerm  Term
}

// RequestVoteReply is the RequestVote RPC response (Figure 2).
type RequestVoteReply struct {
	Term        Term
	VoteGranted bool
}

// AppendEntriesArgs is the AppendEntries RPC request (Figure 2).
type AppendEntriesArgs struct {
	Term         Term
	LeaderID     NodeID
	PrevLogIndex LogIndex
	PrevLogTerm  Term
	Entries      []LogEntry
	LeaderCommit LogIndex
}

// AppendEntriesReply is the AppendEntries RPC response (Figure 2).
type AppendEntriesReply struct {
	Term    Term
	Success bool
}
