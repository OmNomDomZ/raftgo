package raft

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrNotLeader is returned, as a *NotLeaderError, by Start and ReadIndex on
// a node that is not the leader.
var ErrNotLeader = errors.New("raft: not the leader")

// NotLeaderError is the error Start and ReadIndex return on a node that is
// not the leader; errors.Is(err, ErrNotLeader) reports true for it.
type NotLeaderError struct {
	// Leader is the leader this node knows of in its current term, or "" if
	// it knows none. It is only a hint: that node may have lost its term
	// since.
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

// Start appends cmd to the leader's log and returns the index and term it
// will be committed at, if it is committed at all: a leader can lose its
// term before replicating it. The caller watches ApplyCh for a message at
// that index and term. cmd is shared, not copied, so the caller must not
// modify it afterwards.
func (rf *Raft) Start(cmd []byte) (Index, Term, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if !rf.isLeaderLocked() {
		return 0, rf.currentTerm, rf.notLeaderErrLocked()
	}
	index := rf.lastLogIndexLocked() + 1
	entry := LogEntry{Term: rf.currentTerm, Kind: EntryCommand, Command: cmd}
	if err := rf.storeLogLocked(index, []LogEntry{entry}); err != nil {
		// TODO: step down here. Heartbeats write nothing, so a leader whose
		// disk has failed keeps passing CheckQuorum and holding followers'
		// election timers back, while every Start fails: the cluster cannot
		// take writes until this node dies. Once deposed it cannot win again,
		// since startElectionLocked needs SaveState to succeed.
		return 0, rf.currentTerm, fmt.Errorf("raft: persist entry %d: %w", index, err)
	}
	rf.advanceCommitLocked()
	rf.wakeReplicatorsLocked()
	return index, rf.currentTerm, nil
}

func (rf *Raft) onHeartbeatTimeout(ctx context.Context) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// A tick that raced with stepping down. A stale tick while still Leader
	// only sends one extra heartbeat, which is harmless.
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
		default: // a wake-up is already pending
		}
	}
}

// replicate runs for the node's lifetime and, while this node is Leader,
// sends p an AppendEntries on every wake-up, then keeps sending without
// waiting while p's log is behind. Keeping at most one AppendEntries in
// flight per peer means a slow peer coalesces wake-ups instead of piling up
// goroutines, and p receives entries in order.
func (rf *Raft) replicate(ctx context.Context, p peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}

		for {
			rf.mu.Lock()
			// A wake-up can outlive the leadership it was sent for.
			if rf.stoppedLocked() || rf.role != Leader {
				rf.mu.Unlock()
				break
			}
			rpcCtx := rf.rpcContextLocked(ctx)
			args, round := rf.appendEntriesArgsLocked(p.id)
			rf.mu.Unlock()

			if !rf.sendAppendEntries(rpcCtx, p.id, args, round) {
				break
			}
		}
	}
}

// appendEntriesArgsLocked builds the next AppendEntries for peer from its
// nextIndex (Figure 2). It also returns the latest read round, which an answer
// to this request confirms; see ReadIndex.
func (rf *Raft) appendEntriesArgsLocked(peer NodeID) (*AppendEntriesArgs, uint64) {
	next := rf.nextIndex[peer]
	end := min(rf.lastLogIndexLocked()+1, next+Index(rf.maxEntriesPerRPC))
	return &AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderID:     rf.id,
		PrevLogIndex: next - 1,
		PrevLogTerm:  rf.termAtLocked(next - 1),
		Entries:      rf.entriesLocked(next, end),
		LeaderCommit: rf.commitIndex,
	}, rf.readRound
}

// sendAppendEntries sends args to peer and handles the reply; round is the
// read round args confirms. It reports whether peer needs another
// AppendEntries right away: its log is still behind, or it rejected args and
// nextIndex has backed off.
func (rf *Raft) sendAppendEntries(ctx context.Context, peer NodeID, args *AppendEntriesArgs, round uint64) (again bool) {
	// Waiting long on a lost AppendEntries would hold back the next and let
	// the peer's election timer fire; see Config.AppendEntriesTimeout.
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
	// Any answer in our term, success or not, shows that peer has not moved
	// on to a later term, so it still backs this leader.
	rf.heardFrom[peer] = struct{}{}
	if round > rf.ackedRound[peer] {
		rf.ackedRound[peer] = round
		rf.resolveReadsLocked()
	}

	if reply.Success {
		// peer's log now matches ours through the last entry sent (§5.3).
		if match := args.PrevLogIndex + Index(len(args.Entries)); match > rf.matchIndex[peer] {
			rf.matchIndex[peer] = match
			rf.advanceCommitLocked()
		}
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1
		return rf.nextIndex[peer] <= rf.lastLogIndexLocked()
	}

	if args.PrevLogIndex <= rf.matchIndex[peer] {
		// peer already matched a longer prefix of our log in this term, and
		// only a leader of a later term can change it (Log Matching, §5.3).
		// Every heartbeat resends the same request, so report it once.
		if _, logged := rf.mismatchLogged[peer]; !logged {
			rf.mismatchLogged[peer] = struct{}{}
			rf.logger.Error("peer rejected a prefix it had matched (Log Matching, §5.3)",
				"peer", peer, "term", args.Term, "prevLogIndex", args.PrevLogIndex, "matchIndex", rf.matchIndex[peer])
		}
		return false
	}
	// Skip back over the whole conflicting term instead of one entry per
	// round trip (§5.3). If we have entries of peer's conflicting term, our
	// logs may match up to our last one of them.
	next := reply.ConflictIndex
	if reply.ConflictTerm != 0 {
		if last, ok := rf.lastIndexOfTermLocked(reply.ConflictTerm); ok {
			next = last + 1
		}
	}
	// Back off by at least one entry, so repair always makes progress, but
	// never below what peer is known to match.
	rf.nextIndex[peer] = max(min(next, args.PrevLogIndex), rf.matchIndex[peer]+1)
	return true
}

// advanceCommitLocked commits the highest index stored on a quorum, but only
// if it is from the current term: an entry of an earlier term can be stored
// on a majority and still be overwritten (§5.4.2, Figure 8). Committing it
// commits every earlier entry too, by Log Matching.
func (rf *Raft) advanceCommitLocked() {
	// Our own log counts too: storeLogLocked makes it durable before
	// adopting it.
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
	rf.resolveReadsLocked()
}

// checkAppendEntriesReply rejects replies no correct peer sends; see
// checkVoteReply. A peer that rejects a leader of its own term must have
// found a mismatch at PrevLogIndex, and index 0 always matches (§5.3).
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
