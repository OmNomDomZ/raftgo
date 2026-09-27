// Package raft implements the Raft consensus protocol.
package raft

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
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
	DefaultHeartbeatInterval = 50 * time.Millisecond
	DefaultRPCTimeout        = 200 * time.Millisecond
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

	// ElectionTimeout must return a freshly randomized duration on every call
	// (§5.2). Default: uniform in [150ms, 300ms).
	ElectionTimeout func() time.Duration
	// HeartbeatInterval must stay well below the minimum election timeout, or
	// followers will start elections while the leader is healthy (§5.2).
	HeartbeatInterval time.Duration
	RPCTimeout        time.Duration

	Logger *slog.Logger // default discards everything
}

func (c *Config) validateAndSetDefaults() error {
	var errs []error
	if c.ID == "" {
		errs = append(errs, errors.New("ID is required"))
	}
	if slices.Contains(c.Peers, c.ID) {
		errs = append(errs, fmt.Errorf("peers must not contain own ID %q", c.ID))
	}
	if c.Transport == nil {
		errs = append(errs, errors.New("transport is required"))
	}
	if c.Storage == nil {
		errs = append(errs, errors.New("storage is required"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("raft: invalid config: %w", err)
	}

	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	if c.ElectionTimeout == nil {
		c.ElectionTimeout = func() time.Duration {
			return defaultElectionMin + rand.N(defaultElectionMax-defaultElectionMin)
		}
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if c.RPCTimeout == 0 {
		c.RPCTimeout = DefaultRPCTimeout
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return nil
}

// Raft is a single Raft node.
type Raft struct {
	// Immutable after New.
	id                NodeID
	peers             []NodeID
	transport         Transport
	storage           Storage
	electionTimeout   func() time.Duration
	heartbeatInterval time.Duration
	rpcTimeout        time.Duration
	logger            *slog.Logger
	electionTimer     Timer // stopped while Leader
	heartbeatTimer    Timer // running only while Leader
	applyCh           chan ApplyMsg

	// wg tracks outgoing RPC goroutines so Run can wait for them on exit.
	wg sync.WaitGroup

	mu            sync.Mutex
	role          Role
	currentTerm   Term
	votedFor      NodeID
	votesReceived int // meaningful only while Candidate
	// dirty means currentTerm/votedFor are ahead of storage; see
	// becomeFollowerLocked.
	dirty bool
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
		peers:             slices.Clone(cfg.Peers),
		transport:         cfg.Transport,
		storage:           cfg.Storage,
		electionTimeout:   cfg.ElectionTimeout,
		heartbeatInterval: cfg.HeartbeatInterval,
		rpcTimeout:        cfg.RPCTimeout,
		logger:            cfg.Logger.With("node", cfg.ID),
		electionTimer:     cfg.Clock.NewTimer(0),
		heartbeatTimer:    cfg.Clock.NewTimer(0),
		applyCh:           make(chan ApplyMsg),
		role:              Follower,
		currentTerm:       state.CurrentTerm,
		votedFor:          state.VotedFor,
	}
	// The Timer interface has no way to create a stopped timer; Run arms the
	// election timer.
	rf.electionTimer.Stop()
	rf.heartbeatTimer.Stop()
	return rf, nil
}

// ApplyMsg is a committed log entry delivered to the state machine.
type ApplyMsg struct {
	Index   LogIndex
	Term    Term
	Command []byte
}

// ApplyCh returns the channel committed entries are delivered on. The caller
// must keep receiving from it.
func (rf *Raft) ApplyCh() <-chan ApplyMsg {
	return rf.applyCh
}

// GetState reports the current term and whether this node believes it is
// the leader.
func (rf *Raft) GetState() (term Term, isLeader bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.role == Leader
}

// Start proposes cmd for replication and returns without waiting for commit;
// the outcome is delivered on ApplyCh. isLeader=false means the caller should
// retry on another node.
func (rf *Raft) Start(cmd []byte) (index LogIndex, term Term, isLeader bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// TODO: append cmd to the log and replicate it.
	return 0, rf.currentTerm, rf.role == Leader
}

// quorum is the number of votes needed for a majority, counting this node.
func (rf *Raft) quorum() int {
	return (len(rf.peers)+1)/2 + 1
}

func (rf *Raft) becomeLeaderLocked() {
	rf.role = Leader
	rf.electionTimer.Stop()
	// Fire immediately to assert leadership without waiting a full interval.
	rf.heartbeatTimer.Reset(0)
}

// becomeFollowerLocked adopts term and steps down even if the new term cannot
// be persisted. Role is volatile and stepping down only restricts what the
// node may do, so keeping a stale Candidate/Leader alive would only hurt
// liveness. Replies that commit the node to the new term go through
// syncLocked, so storage failures never leak out as promises.
func (rf *Raft) becomeFollowerLocked(term Term) {
	rf.currentTerm = term
	rf.votedFor = ""
	rf.dirty = true
	_ = rf.syncLocked() // best effort; the failure is logged by persistLocked

	wasLeader := rf.role == Leader
	rf.role = Follower
	if wasLeader {
		rf.heartbeatTimer.Stop()
		rf.electionTimer.Reset(rf.electionTimeout())
	}
}
