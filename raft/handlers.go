package raft

import (
	"errors"
	"fmt"
)

var _ RPCHandler = (*Raft)(nil)

// ErrStopped is returned by the RPC handlers once Run's context is done. A
// stopped node answers nothing, as if it had crashed: a rejection would carry
// meaning, e.g. Success=false means a log mismatch to the leader (§5.3).
var ErrStopped = errors.New("raft: node is stopped")

// ErrInvalidArgs is returned by the RPC handlers for a malformed request,
// which is answered with nothing, as if it had been lost.
var ErrInvalidArgs = errors.New("raft: invalid RPC arguments")

// HandleRequestVote implements the RequestVote RPC receiver (Figure 2).
func (rf *Raft) HandleRequestVote(args *RequestVoteArgs) (*RequestVoteReply, error) {
	// An empty CandidateID reads as "no vote" in votedFor, so granting it would
	// let the node vote again in the same term (Election Safety).
	if args == nil || args.CandidateID == "" {
		return nil, fmt.Errorf("%w: RequestVote without a candidate ID", ErrInvalidArgs)
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return nil, ErrStopped
	}
	if args.Term < rf.currentTerm {
		return &RequestVoteReply{Term: rf.currentTerm, VoteGranted: false}, nil
	}
	if args.Term > rf.currentTerm {
		rf.adoptHigherTermLocked(args.Term)
	}

	// TODO(3B): also require the candidate's log to be at least as up-to-date
	// as ours (§5.4.1).
	if rf.votedFor != "" && rf.votedFor != args.CandidateID {
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
	if args == nil || args.LeaderID == "" {
		return nil, fmt.Errorf("%w: AppendEntries without a leader ID", ErrInvalidArgs)
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.stoppedLocked() {
		return nil, ErrStopped
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
			// crash this node; reject it and keep our term and role.
			rf.logger.Error("another leader in our term, rejecting (Election Safety, §5.2)",
				"term", args.Term, "otherLeader", args.LeaderID)
			return &AppendEntriesReply{Term: rf.currentTerm, Success: false}, nil
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

	// TODO(3B): log consistency check and entry append (§5.3).
	return &AppendEntriesReply{Term: rf.currentTerm, Success: true}, nil
}
