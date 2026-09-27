package raft

import (
	"errors"
	"fmt"
	"math"
)

var _ RPCHandler = (*Raft)(nil)

// ErrStopped is returned by the RPC handlers once Run's context is done. A
// stopped node answers nothing, as if it had crashed: a rejection would carry
// meaning, e.g. Success=false means a log mismatch to the leader (§5.3).
var ErrStopped = errors.New("raft: node is stopped")

// ErrNotStarted is returned by the RPC handlers before Run is called. The
// node answers nothing until then, so it cannot vote or acknowledge a leader
// while nothing drives its timers.
var ErrNotStarted = errors.New("raft: node is not started")

// ErrInvalidArgs is returned by the RPC handlers for a malformed request,
// which is answered with nothing, as if it had been lost.
var ErrInvalidArgs = errors.New("raft: invalid RPC arguments")

// ErrLeaderConflict is returned by HandleAppendEntries when this node is the
// leader of the request's term. Election Safety is already broken then, and
// any reply would be misread: Success=false means a log mismatch (§5.3).
var ErrLeaderConflict = errors.New("raft: another leader in this term")

// ErrCommittedConflict is returned by HandleAppendEntries when the request
// would overwrite a committed entry. State Machine Safety is already broken
// then (§5.4.3); the entry may have been applied, so the node keeps its log
// and answers nothing.
var ErrCommittedConflict = errors.New("raft: request conflicts with a committed entry")

// HandleRequestVote implements the RequestVote RPC receiver (Figure 2).
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
	// A candidate whose log lacks an entry we have cannot hold every committed
	// entry, so it must not win (Leader Completeness, §5.4.1). A denial is no
	// reason to hold off our own election, so the timer is not reset.
	if !rf.upToDateLocked(args.LastLogIndex, args.LastLogTerm) {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}, nil
	}

	// Also makes a term adopted by stepping down durable. A retried request
	// from the candidate we already voted for needs no write.
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

// HandleAppendEntries implements the AppendEntries RPC receiver (Figure 2).
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
			// Two leaders in one term break Election Safety (§5.2), so one of
			// us is buggy. The request comes from the network, so it must not
			// crash this node; answer nothing and keep our term and role.
			rf.logger.Error("another leader in our term, not answering (Election Safety, §5.2)",
				"term", args.Term, "otherLeader", args.LeaderID)
			return nil, fmt.Errorf("%w: %s in term %d", ErrLeaderConflict, args.LeaderID, args.Term)
		case Candidate:
			// Another node already won this term's election (§5.2).
			rf.logger.Info("stepping down, another node won the election",
				"term", rf.currentTerm, "leader", args.LeaderID)
			rf.role = Follower
			rf.cancelRPCsLocked()
		}
	}
	// The leader is legitimate whether or not we can persist, so there is no
	// reason to start an election against it.
	rf.resetElectionTimerLocked()

	// Success acknowledges the leader of this term. If our term is not
	// durable, a restart could roll it back, so there must be no reply at all:
	// Success=false would mean a log mismatch to the leader (§5.3).
	if err := rf.syncLocked(); err != nil {
		return nil, fmt.Errorf("raft: persist term %d: %w", rf.currentTerm, err)
	}

	return rf.appendEntriesLocked(args)
}

// appendEntriesLocked is the log part of the AppendEntries receiver
// (Figure 2, §5.3), for a request from the leader of our current term.
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

	// Only truncate at the first entry that actually conflicts. A delayed or
	// duplicated request carries a prefix of what we already have, and
	// truncating after it would drop entries we may have acknowledged.
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
		// Success tells the leader the entries are stored, so they must be
		// durable before the reply (§5.3).
		if err := rf.storeLogLocked(from, args.Entries[skip:]); err != nil {
			return nil, fmt.Errorf("raft: persist entries from %d in term %d: %w", from, rf.currentTerm, err)
		}
	}

	// Entries past the request may not match the leader's log yet, so the
	// leader's commit index only covers the last entry it sent (Figure 2).
	if commit := min(args.LeaderCommit, args.PrevLogIndex+Index(len(args.Entries))); commit > rf.commitIndex {
		rf.commitIndex = commit
	}
	return &AppendEntriesReply{Term: rf.currentTerm, Success: true}, nil
}

// upToDateLocked reports whether a log ending with an entry of lastTerm at
// lastIndex is at least as up-to-date as ours (§5.4.1).
func (rf *Raft) upToDateLocked(lastIndex Index, lastTerm Term) bool {
	if ours := rf.lastLogTermLocked(); lastTerm != ours {
		return lastTerm > ours
	}
	return lastIndex >= rf.lastLogIndexLocked()
}

// runningLocked reports why the node cannot answer RPCs, if it cannot.
func (rf *Raft) runningLocked() error {
	switch {
	case !rf.started:
		return ErrNotStarted
	case rf.stoppedLocked():
		return ErrStopped
	}
	return nil
}

// checkRequestVoteArgs rejects requests no correct candidate sends. A
// candidate increments its term before asking for votes, so terms start at 1
// (§5.2), and no node sends RPCs to itself.
func (rf *Raft) checkRequestVoteArgs(args *RequestVoteArgs) error {
	switch {
	case args == nil:
		return fmt.Errorf("%w: nil RequestVote", ErrInvalidArgs)
	// An empty CandidateID reads as "no vote" in votedFor, so granting it
	// would let the node vote again in the same term (Election Safety).
	case args.CandidateID == "":
		return fmt.Errorf("%w: RequestVote without a candidate ID", ErrInvalidArgs)
	case args.CandidateID == rf.id:
		return fmt.Errorf("%w: RequestVote from this node itself", ErrInvalidArgs)
	case args.Term == 0:
		return fmt.Errorf("%w: RequestVote from %s in term 0", ErrInvalidArgs, args.CandidateID)
	// A candidate's log only holds entries of terms up to its own, and the
	// entry at index 0 alone has term 0.
	case args.LastLogTerm > args.Term:
		return fmt.Errorf("%w: RequestVote from %s in term %d with last log term %d",
			ErrInvalidArgs, args.CandidateID, args.Term, args.LastLogTerm)
	case (args.LastLogIndex == 0) != (args.LastLogTerm == 0):
		return fmt.Errorf("%w: RequestVote from %s with last log entry %d of term %d",
			ErrInvalidArgs, args.CandidateID, args.LastLogIndex, args.LastLogTerm)
	}
	return nil
}

// checkAppendEntriesArgs rejects requests no correct leader sends; see
// checkRequestVoteArgs. A leader was elected in its term, so it is never 0,
// and its log is a valid log whose terms are at most its own.
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
