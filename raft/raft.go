// Package raft implements the Raft consensus protocol.
package raft

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Role is a node's role in the cluster (§5.1).
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// peer is another cluster member as seen by this node.
type peer struct {
	id NodeID
	// wake wakes the peer's replicator; see replicate.
	wake chan struct{}
}

// Raft is a single Raft node.
type Raft struct {
	// Immutable after New.
	id                   NodeID
	peers                []peer
	transport            Transport
	storage              Storage
	clock                Clock
	electionTimeout      func() time.Duration
	heartbeatInterval    time.Duration
	requestVoteTimeout   time.Duration
	appendEntriesTimeout time.Duration
	maxEntriesPerRPC     int
	applyCh              chan<- ApplyMsg
	// applyWake wakes the applier; see apply.
	applyWake      chan struct{}
	logger         *slog.Logger
	onFatal        func(msg string)
	electionTimer  Timer // paces quorum checks while Leader; see checkQuorumLocked
	heartbeatTimer Timer // running only while Leader
	// onBecomeLeader, if set, is called under mu whenever the node becomes
	// Leader. Tests use it to check Election Safety and Leader Completeness
	// over a node's whole history rather than at sampled moments.
	onBecomeLeader func(term Term)

	// wg tracks the goroutines Run waits for on exit. wg.Go must only be
	// called from Run's goroutine (directly or from its timer callbacks), so
	// it never races with the final wg.Wait; RPC handlers and reply
	// goroutines must not start tracked goroutines.
	wg sync.WaitGroup

	mu          sync.Mutex
	started     bool
	done        <-chan struct{} // Run's ctx.Done(); nil before Run
	role        Role
	currentTerm Term
	votedFor    NodeID
	// leaderID is the leader of currentTerm, if this node knows it: itself
	// while Leader, or whoever it last accepted an AppendEntries from. It is
	// only a hint for clients, so it is volatile.
	leaderID NodeID
	// votes holds the peers that granted a vote in currentTerm, including
	// this node; meaningful only while Candidate. A set rather than a counter,
	// so a duplicated grant (e.g. from a transport retry) is counted once.
	votes map[NodeID]struct{}
	// rpcCtx scopes outgoing RPCs to the current term and role, so RPCs sent
	// for an earlier one are cancelled instead of running out their timeout. nil
	// until the next send creates it; see cancelRPCsLocked.
	rpcCtx    context.Context
	rpcCancel context.CancelFunc
	// dirty means currentTerm/votedFor are ahead of storage; see
	// adoptHigherTermLocked.
	dirty bool
	// electionDeadline is when the armed election timer is due; see
	// onElectionTimeout.
	electionDeadline time.Time

	// log is always durable: storeLogLocked writes it before adopting it.
	// See log.go for its layout.
	log []LogEntry
	// commitIndex is the highest index known to be committed. It is
	// volatile: a restarted node learns it again from the leader, or as
	// leader by committing an entry of its own term.
	commitIndex Index
	// nextIndex and matchIndex are the leader's view of each peer's log
	// (Figure 2), reset on every election win; see initLeaderStateLocked.
	nextIndex  map[NodeID]Index
	matchIndex map[NodeID]Index
	// heardFrom holds the peers that have answered this leader since its
	// last quorum check; see checkQuorumLocked.
	heardFrom map[NodeID]struct{}
	// mismatchLogged holds the peers already reported for rejecting a prefix
	// they had matched, so a broken peer is logged once per term instead of
	// on every heartbeat.
	mismatchLogged map[NodeID]struct{}
	// readRound numbers the requests to confirm leadership for reads, and
	// ackedRound is the latest round each peer has answered in this term.
	// reads are the ReadIndex calls waiting on them; see read.go.
	readRound  uint64
	ackedRound map[NodeID]uint64
	reads      []*pendingRead
}

// New creates a node, restoring its term and vote from cfg.Storage if a
// previous incarnation saved them. The node starts as a Follower; call Run to
// start it.
func New(cfg Config) (*Raft, error) {
	if err := cfg.validateAndSetDefaults(); err != nil {
		return nil, err
	}

	state, err := cfg.Storage.LoadState()
	if err != nil {
		return nil, fmt.Errorf("raft: load state: %w", err)
	}
	entries, err := cfg.Storage.LoadLog()
	if err != nil {
		return nil, fmt.Errorf("raft: load log: %w", err)
	}
	// A node only stores entries of terms it has already adopted durably, so
	// anything else means the storage is corrupt.
	if err := checkEntries(0, entries, state.CurrentTerm); err != nil {
		return nil, fmt.Errorf("raft: invalid stored log: %w", err)
	}

	rf := &Raft{
		id:                   cfg.ID,
		transport:            cfg.Transport,
		storage:              cfg.Storage,
		clock:                cfg.Clock,
		electionTimeout:      cfg.electionTimeout,
		heartbeatInterval:    cfg.HeartbeatInterval,
		requestVoteTimeout:   cfg.RequestVoteTimeout,
		appendEntriesTimeout: cfg.AppendEntriesTimeout,
		maxEntriesPerRPC:     cfg.MaxEntriesPerRPC,
		applyCh:              cfg.ApplyCh,
		applyWake:            make(chan struct{}, 1),
		logger:               cfg.Logger.With("node", cfg.ID),
		onFatal:              cfg.OnFatal,
		electionTimer:        cfg.Clock.NewTimer(0),
		heartbeatTimer:       cfg.Clock.NewTimer(0),
		role:                 Follower,
		currentTerm:          state.CurrentTerm,
		votedFor:             state.VotedFor,
		log:                  append([]LogEntry{{}}, entries...),
	}
	// The Timer interface has no way to create a stopped timer; Run arms the
	// election timer.
	rf.electionTimer.Stop()
	rf.heartbeatTimer.Stop()
	rf.peers = make([]peer, len(cfg.Peers))
	for i, id := range cfg.Peers {
		rf.peers[i] = peer{id: id, wake: make(chan struct{}, 1)}
	}
	return rf, nil
}

// GetState reports the current term and whether this node believes it is
// the leader. A stopped node is never the leader.
func (rf *Raft) GetState() (term Term, isLeader bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.isLeaderLocked()
}

// Leader returns the leader this node knows of in its current term, or "" if
// it knows none; see NotLeaderError. A stopped node knows none.
func (rf *Raft) Leader() NodeID {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.leaderLocked()
}

func (rf *Raft) leaderLocked() NodeID {
	if rf.stoppedLocked() {
		return ""
	}
	return rf.leaderID
}

func (rf *Raft) isLeaderLocked() bool {
	return rf.role == Leader && !rf.stoppedLocked()
}

// stoppedLocked reports whether Run's context is done. A stopped node must
// not change its state or promise anything.
func (rf *Raft) stoppedLocked() bool {
	select {
	case <-rf.done:
		return true
	default:
		return false
	}
}

// fatalf reports a broken protocol invariant through Config.OnFatal.
// Carrying on could violate safety, so the node must not keep running.
func (rf *Raft) fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rf.logger.Error("invariant violated", "msg", msg)
	rf.onFatal("raft: " + msg)
	// OnFatal broke its contract by returning; a panic is the last resort.
	panic("raft: OnFatal returned after: " + msg)
}

// quorum is the number of votes needed for a majority, counting this node.
func (rf *Raft) quorum() int {
	return (len(rf.peers)+1)/2 + 1
}

// becomeLeaderLocked is called once a Candidate has a quorum of votes.
func (rf *Raft) becomeLeaderLocked() {
	// The no-op must be durable before the node acts as leader, since
	// counting its own log towards commitment assumes that. If it cannot be
	// stored, the node stays Candidate: the next grant or election retries,
	// and until then there is no leader in this term, which is always safe.
	noop := LogEntry{Term: rf.currentTerm, Kind: EntryNoOp}
	if err := rf.storeLogLocked(rf.lastLogIndexLocked()+1, []LogEntry{noop}); err != nil {
		return
	}
	rf.logger.Info("became leader", "term", rf.currentTerm, "votes", len(rf.votes),
		"lastLogIndex", rf.lastLogIndexLocked())
	rf.role = Leader
	rf.leaderID = rf.id
	rf.initLeaderStateLocked()
	if rf.onBecomeLeader != nil {
		rf.onBecomeLeader(rf.currentTerm)
	}
	// Outstanding vote requests are no longer needed.
	rf.cancelRPCsLocked()
	// From now on the timer schedules quorum checks instead of elections.
	rf.resetElectionTimerLocked()
	// A single-node cluster commits the no-op right away.
	rf.advanceCommitLocked()
	// Fire immediately to assert leadership without waiting a full interval.
	rf.heartbeatTimer.Reset(0)
}

// initLeaderStateLocked resets the leader's view of its peers (Figure 2):
// it knows nothing about their logs yet, so it optimistically starts
// replicating right after its own last entry.
func (rf *Raft) initLeaderStateLocked() {
	rf.nextIndex = make(map[NodeID]Index, len(rf.peers))
	rf.matchIndex = make(map[NodeID]Index, len(rf.peers))
	for _, p := range rf.peers {
		rf.nextIndex[p.id] = rf.lastLogIndexLocked() + 1
		rf.matchIndex[p.id] = 0
	}
	rf.heardFrom = make(map[NodeID]struct{}, len(rf.peers))
	rf.mismatchLogged = make(map[NodeID]struct{})
	rf.readRound = 0
	rf.ackedRound = make(map[NodeID]uint64, len(rf.peers))
}

// adoptHigherTermLocked moves to a newer term and steps down (§5.1). It does
// not persist: role is volatile and stepping down only restricts what the
// node may do, so it must not wait for storage. Replies that commit the node
// to the new term go through syncLocked, so a storage failure never leaks out
// as a promise.
func (rf *Raft) adoptHigherTermLocked(term Term) {
	if term <= rf.currentTerm {
		// Clearing votedFor within the current term would let the node vote
		// twice in it (Election Safety).
		rf.fatalf("adopting term %d, but current term is already %d", term, rf.currentTerm)
	}
	rf.logger.Info("adopted higher term, stepping down",
		"oldTerm", rf.currentTerm, "term", term, "oldRole", rf.role)
	rf.currentTerm = term
	rf.votedFor = ""
	rf.leaderID = ""
	rf.dirty = true
	rf.cancelRPCsLocked()

	if rf.role == Leader {
		rf.stepDownLocked()
	} else {
		rf.role = Follower
	}
}

// checkQuorumLocked runs on every election timeout of a leader and steps it
// down unless a quorum has answered it since the previous one (CheckQuorum,
// §6.2 of Ongaro's thesis). A leader cut off in a minority would otherwise
// keep accepting commands it can never commit until the partition heals.
func (rf *Raft) checkQuorumLocked() {
	if len(rf.heardFrom)+1 >= rf.quorum() {
		clear(rf.heardFrom)
		rf.resetElectionTimerLocked()
		return
	}
	rf.logger.Info("lost contact with a quorum, stepping down",
		"term", rf.currentTerm, "heardFrom", len(rf.heardFrom))
	rf.stepDownLocked()
}

// stepDownLocked turns a leader into a follower of its current term.
func (rf *Raft) stepDownLocked() {
	rf.role = Follower
	rf.leaderID = ""
	rf.heartbeatTimer.Stop()
	rf.resetElectionTimerLocked()
	rf.cancelRPCsLocked()
	rf.failReadsLocked(rf.notLeaderErrLocked())
}

func (rf *Raft) resetElectionTimerLocked() {
	d := rf.electionTimeout()
	rf.electionDeadline = rf.clock.Now().Add(d)
	rf.electionTimer.Reset(d)
}

// rpcContextLocked returns the context for RPCs sent on behalf of the current
// term and role, deriving it from parent (Run's context) if needed.
func (rf *Raft) rpcContextLocked(parent context.Context) context.Context {
	if rf.rpcCtx == nil {
		rf.rpcCtx, rf.rpcCancel = context.WithCancel(parent)
	}
	return rf.rpcCtx
}

// withTimeout is like context.WithTimeout, but on rf.clock rather than on
// wall time, so RPC deadlines follow the same clock as the node's timers and
// tests can drive both. Once the deadline passes, ctx.Err() is
// context.Canceled and context.Cause(ctx) is context.DeadlineExceeded. The
// goroutine is not tracked by wg: it exits as soon as the caller's deferred
// cancel runs.
func (rf *Raft) withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := rf.clock.NewTimer(d)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C():
			cancel(context.DeadlineExceeded)
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

// cancelRPCsLocked aborts in-flight RPCs; it must be called whenever the term
// or role changes. Their replies would be discarded anyway, so this only
// frees goroutines and transport resources early.
func (rf *Raft) cancelRPCsLocked() {
	if rf.rpcCancel != nil {
		rf.rpcCancel()
		rf.rpcCtx, rf.rpcCancel = nil, nil
	}
}
