// Package raft implements the Raft consensus protocol.
package raft

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
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

const (
	defaultHeartbeatInterval = 50 * time.Millisecond
	defaultRPCTimeout        = 200 * time.Millisecond
	defaultElectionMin       = 150 * time.Millisecond
	defaultElectionMax       = 300 * time.Millisecond
)

// Config configures a node. ID, Peers, Transport and Storage are required;
// zero values of the other fields select defaults.
type Config struct {
	ID    NodeID
	Peers []NodeID // other cluster members, excluding ID

	Transport Transport
	Storage   Storage
	Clock     Clock // default SystemClock

	// The election timeout is drawn uniformly from [ElectionTimeoutMin,
	// ElectionTimeoutMax) on every reset, so split votes resolve (§5.2).
	// Default: [150ms, 300ms).
	ElectionTimeoutMin time.Duration
	ElectionTimeoutMax time.Duration
	// Rand is the source of election timeouts. It is only used under the
	// node's lock, so it must not be shared between nodes. Default: the
	// global math/rand/v2 source.
	Rand *rand.Rand
	// HeartbeatInterval must stay well below the minimum election timeout, or
	// followers will start elections while the leader is healthy (§5.2). It
	// also bounds each heartbeat RPC. Default: 50ms.
	HeartbeatInterval time.Duration
	// RPCTimeout bounds each RequestVote RPC. Default: 200ms.
	RPCTimeout time.Duration

	Logger *slog.Logger // default discards everything

	// OnFatal is called when a protocol invariant is broken and must not
	// return: carrying on could violate safety. Default: print msg to stderr
	// and exit with status 1, so the reason survives the discarding default
	// Logger. A panic is not a safe default, since a transport serving RPCs
	// may recover it (net/http does) and leave the node running.
	OnFatal func(msg string)

	// electionTimeout, if set, replaces the randomized election timeout so
	// tests can script which node times out first. A duration outside
	// [ElectionTimeoutMin, ElectionTimeoutMax) is reported through OnFatal.
	electionTimeout func() time.Duration
}

func (c *Config) validateAndSetDefaults() error {
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}

	var errs []error
	if c.ID == "" {
		errs = append(errs, errors.New("ID is required"))
	}
	if slices.Contains(c.Peers, c.ID) {
		errs = append(errs, fmt.Errorf("peers must not contain own ID %q", c.ID))
	}
	if slices.Contains(c.Peers, "") {
		errs = append(errs, errors.New("peers must not contain an empty ID"))
	}
	// A duplicated peer inflates the quorum and lets its votes count more than
	// once, so a candidate could win without a real majority (Election Safety).
	if dups := duplicates(c.Peers); len(dups) > 0 {
		errs = append(errs, fmt.Errorf("duplicate peers %v", dups))
	}
	if c.Transport == nil {
		errs = append(errs, errors.New("transport is required"))
	}
	if c.Storage == nil {
		errs = append(errs, errors.New("storage is required"))
	}
	if c.HeartbeatInterval < 0 {
		errs = append(errs, fmt.Errorf("negative heartbeat interval %v", c.HeartbeatInterval))
	}
	if c.RPCTimeout < 0 {
		errs = append(errs, fmt.Errorf("negative RPC timeout %v", c.RPCTimeout))
	}
	if c.ElectionTimeoutMin < 0 || c.ElectionTimeoutMax < 0 {
		errs = append(errs, fmt.Errorf("negative election timeout bounds [%v, %v)",
			c.ElectionTimeoutMin, c.ElectionTimeoutMax))
	}
	electionMin := cmp.Or(c.ElectionTimeoutMin, defaultElectionMin)
	electionMax := cmp.Or(c.ElectionTimeoutMax, defaultElectionMax)
	// An empty range would give every node the same timeout, and split votes
	// could repeat forever (§5.2).
	if electionMax <= electionMin {
		errs = append(errs, fmt.Errorf("election timeout max %v must be above min %v", electionMax, electionMin))
	}
	if hb := cmp.Or(c.HeartbeatInterval, defaultHeartbeatInterval); hb >= electionMin {
		errs = append(errs, fmt.Errorf("heartbeat interval %v must be below the minimum election timeout %v",
			hb, electionMin))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("raft: invalid config: %w", err)
	}

	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	c.ElectionTimeoutMin, c.ElectionTimeoutMax = electionMin, electionMax
	if c.electionTimeout == nil {
		span := electionMax - electionMin
		if r := c.Rand; r != nil {
			c.electionTimeout = func() time.Duration { return electionMin + time.Duration(r.Int64N(int64(span))) }
		} else {
			c.electionTimeout = func() time.Duration { return electionMin + rand.N(span) }
		}
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = defaultHeartbeatInterval
	}
	if c.RPCTimeout == 0 {
		c.RPCTimeout = defaultRPCTimeout
	}
	if c.OnFatal == nil {
		c.OnFatal = func(msg string) {
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(1)
		}
	}
	return nil
}

// duplicates returns each ID that occurs in ids more than once.
func duplicates(ids []NodeID) []NodeID {
	seen := make(map[NodeID]int, len(ids))
	var dups []NodeID
	for _, id := range ids {
		seen[id]++
		if seen[id] == 2 {
			dups = append(dups, id)
		}
	}
	return dups
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
	id                NodeID
	peers             []peer
	transport         Transport
	storage           Storage
	clock             Clock
	electionTimeout   func() time.Duration
	electionMin       time.Duration
	electionMax       time.Duration
	heartbeatInterval time.Duration
	rpcTimeout        time.Duration
	logger            *slog.Logger
	onFatal           func(msg string)
	electionTimer     Timer // stopped while Leader
	heartbeatTimer    Timer // running only while Leader
	// onBecomeLeader, if set, is called under mu whenever the node becomes
	// Leader. Tests use it to check Election Safety over a node's whole
	// history rather than at sampled moments.
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
	// votes holds the peers that granted a vote in currentTerm, including
	// this node; meaningful only while Candidate. A set rather than a counter,
	// so a duplicated grant (e.g. from a transport retry) is counted once.
	votes map[NodeID]struct{}
	// rpcCtx scopes outgoing RPCs to the current term and role, so RPCs sent
	// for an earlier one are cancelled instead of running out rpcTimeout. nil
	// until the next send creates it; see cancelRPCsLocked.
	rpcCtx    context.Context
	rpcCancel context.CancelFunc
	// dirty means currentTerm/votedFor are ahead of storage; see
	// adoptHigherTermLocked.
	dirty bool
	// electionDeadline is when the armed election timer is due; see
	// onElectionTimeout.
	electionDeadline time.Time
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

	rf := &Raft{
		id:                cfg.ID,
		transport:         cfg.Transport,
		storage:           cfg.Storage,
		clock:             cfg.Clock,
		electionTimeout:   cfg.electionTimeout,
		electionMin:       cfg.ElectionTimeoutMin,
		electionMax:       cfg.ElectionTimeoutMax,
		heartbeatInterval: cfg.HeartbeatInterval,
		rpcTimeout:        cfg.RPCTimeout,
		logger:            cfg.Logger.With("node", cfg.ID),
		onFatal:           cfg.OnFatal,
		electionTimer:     cfg.Clock.NewTimer(0),
		heartbeatTimer:    cfg.Clock.NewTimer(0),
		role:              Follower,
		currentTerm:       state.CurrentTerm,
		votedFor:          state.VotedFor,
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

func (rf *Raft) becomeLeaderLocked() {
	rf.logger.Info("became leader", "term", rf.currentTerm, "votes", len(rf.votes))
	rf.role = Leader
	if rf.onBecomeLeader != nil {
		rf.onBecomeLeader(rf.currentTerm)
	}
	// Outstanding vote requests are no longer needed.
	rf.cancelRPCsLocked()
	rf.electionTimer.Stop()
	// Fire immediately to assert leadership without waiting a full interval.
	rf.heartbeatTimer.Reset(0)
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
	rf.dirty = true
	rf.cancelRPCsLocked()

	wasLeader := rf.role == Leader
	rf.role = Follower
	if wasLeader {
		rf.heartbeatTimer.Stop()
		rf.resetElectionTimerLocked()
	}
}

func (rf *Raft) resetElectionTimerLocked() {
	d := rf.electionTimeout()
	// HeartbeatInterval was only validated against electionMin, and a leader
	// stays up only while its heartbeats beat every follower's timeout (§5.2).
	// The randomized default stays in range by construction; only the test
	// hook can break this.
	if d < rf.electionMin || d >= rf.electionMax {
		rf.fatalf("election timeout %v outside [%v, %v)", d, rf.electionMin, rf.electionMax)
	}
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

// cancelRPCsLocked aborts in-flight RPCs; it must be called whenever the term
// or role changes. Their replies would be discarded anyway, so this only
// frees goroutines and transport resources early.
func (rf *Raft) cancelRPCsLocked() {
	if rf.rpcCancel != nil {
		rf.rpcCancel()
		rf.rpcCtx, rf.rpcCancel = nil, nil
	}
}
