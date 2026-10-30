package raft

import "context"

// Transport delivers outgoing RPCs to peers. Implementations must be safe for
// concurrent use and must return promptly once ctx is done. A nil error means
// the reply is non-nil.
//
// Args and replies must remain immutable after delivery.
type Transport interface {
	RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error)
	AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RPCHandler is a node's inbound RPC surface; *Raft implements it. A non-nil
// error means the node could not answer (e.g. storage failed): the transport
// must fail the RPC for the caller instead of sending a reply, as if the
// message had been lost. Handlers must not modify args.
type RPCHandler interface {
	HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error)
	HandleAppendEntries(args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RequestVoteArgs is the RequestVote RPC request (Figure 2).
type RequestVoteArgs struct {
	Term         Term
	CandidateID  NodeID
	LastLogIndex Index
	LastLogTerm  Term
}

// RequestVoteReply is the RequestVote RPC response (Figure 2).
type RequestVoteReply struct {
	Term        Term
	VoteGranted bool
}

// AppendEntriesArgs is the AppendEntries RPC request (Figure 2).
type AppendEntriesArgs struct {
	Term     Term
	LeaderID NodeID
}

// AppendEntriesReply acknowledges a heartbeat in a term.
type AppendEntriesReply struct {
	Term    Term
	Success bool
}
