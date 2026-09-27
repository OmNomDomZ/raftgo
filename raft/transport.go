package raft

import "context"

// Transport delivers outgoing RPCs to peers. Implementations must be safe for
// concurrent use and must return promptly once ctx is done. A nil error means
// the reply is non-nil.
//
// Args and replies are shared, not copied: the node passes one args value to
// every peer's goroutine, and an in-memory transport may hand it straight to
// the target's handler. Neither side may modify an args or reply value, nor
// anything it references, once it has been passed on. That includes the
// entries of an AppendEntries and their commands.
type Transport interface {
	RequestVote(ctx context.Context, target NodeID, args *RequestVoteArgs) (*RequestVoteReply, error)
	AppendEntries(ctx context.Context, target NodeID, args *AppendEntriesArgs) (*AppendEntriesReply, error)
}

// RPCHandler is a node's inbound RPC surface; *Raft implements it. A non-nil
// error means the node could not answer (e.g. storage failed): the transport
// must fail the RPC for the caller instead of sending a reply, as if the
// message had been lost. Handlers must not modify args or keep references
// into them; see Transport.
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
	Term         Term
	LeaderID     NodeID
	PrevLogIndex Index
	PrevLogTerm  Term
	Entries      []LogEntry // empty for a heartbeat
	LeaderCommit Index
}

// AppendEntriesReply is the AppendEntries RPC response (Figure 2).
//
// When the follower rejects the request because its log does not match at
// PrevLogIndex, ConflictTerm and ConflictIndex let the leader skip back over
// a whole term per round trip instead of one entry (§5.3): ConflictTerm is
// the term of the follower's entry at PrevLogIndex and ConflictIndex the
// first index of that term, or, if the follower's log is shorter than
// PrevLogIndex, ConflictTerm is 0 and ConflictIndex one past its last entry.
type AppendEntriesReply struct {
	Term          Term
	Success       bool
	ConflictTerm  Term
	ConflictIndex Index
}
