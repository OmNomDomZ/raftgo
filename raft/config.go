package raft

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
	"time"
)

const (
	defaultHeartbeatInterval  = 50 * time.Millisecond
	defaultRequestVoteTimeout = 200 * time.Millisecond
	defaultElectionMin        = 150 * time.Millisecond
	defaultElectionMax        = 300 * time.Millisecond
	defaultMaxEntriesPerRPC   = 64
)

// Config configures a node. ID, Peers, Transport, Storage and ApplyCh are
// required; zero values of the other fields select defaults.
type Config struct {
	ID    NodeID
	Peers []NodeID // other cluster members, excluding ID

	Transport Transport
	Storage   Storage
	Clock     Clock // default SystemClock

	// ApplyCh receives every committed entry, in log order; see ApplyMsg.
	// The node sends from its own goroutine without holding its lock, so
	// the consumer may call Start or GetState while handling a message. The
	// node never closes ApplyCh and stops sending once Run returns.
	ApplyCh chan<- ApplyMsg

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
	// followers will start elections while the leader is healthy (§5.2).
	// Default: 50ms.
	HeartbeatInterval time.Duration
	// RequestVoteTimeout bounds each RequestVote RPC. Default: 200ms.
	RequestVoteTimeout time.Duration
	// AppendEntriesTimeout bounds each AppendEntries RPC. At most one is in
	// flight per peer, so a lost one holds back the next for this long; it
	// must stay below the minimum election timeout, or the peer's election
	// timer fires under a healthy leader (§5.2). Raise it when a follower's
	// disk or the network is slow enough that RPCs carrying entries time out.
	// Default: HeartbeatInterval.
	AppendEntriesTimeout time.Duration
	// MaxEntriesPerRPC bounds the entries in one AppendEntries, so catching
	// up a lagging follower does not produce an RPC that cannot finish
	// within AppendEntriesTimeout. Default: 64.
	MaxEntriesPerRPC int

	Logger *slog.Logger // default discards everything

	// OnFatal is called when a protocol invariant is broken and must not
	// return: carrying on could violate safety. Default: print msg to stderr
	// and exit with status 1, so the reason survives the discarding default
	// Logger. A panic is not a safe default, since a transport serving RPCs
	// may recover it (net/http does) and leave the node running.
	OnFatal func(msg string)

	// electionTimeout, if set, replaces the randomized election timeout so
	// tests can script which node times out first.
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
	if c.ApplyCh == nil {
		errs = append(errs, errors.New("apply channel is required"))
	}
	if c.MaxEntriesPerRPC < 0 {
		errs = append(errs, fmt.Errorf("negative max entries per RPC %d", c.MaxEntriesPerRPC))
	}
	if c.HeartbeatInterval < 0 {
		errs = append(errs, fmt.Errorf("negative heartbeat interval %v", c.HeartbeatInterval))
	}
	if c.RequestVoteTimeout < 0 {
		errs = append(errs, fmt.Errorf("negative RequestVote timeout %v", c.RequestVoteTimeout))
	}
	if c.AppendEntriesTimeout < 0 {
		errs = append(errs, fmt.Errorf("negative AppendEntries timeout %v", c.AppendEntriesTimeout))
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
	hb := cmp.Or(c.HeartbeatInterval, defaultHeartbeatInterval)
	if hb >= electionMin {
		errs = append(errs, fmt.Errorf("heartbeat interval %v must be below the minimum election timeout %v",
			hb, electionMin))
	}
	if aet := cmp.Or(c.AppendEntriesTimeout, hb); aet >= electionMin {
		errs = append(errs, fmt.Errorf("AppendEntries timeout %v must be below the minimum election timeout %v",
			aet, electionMin))
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
	c.HeartbeatInterval = hb
	c.AppendEntriesTimeout = cmp.Or(c.AppendEntriesTimeout, hb)
	if c.RequestVoteTimeout == 0 {
		c.RequestVoteTimeout = defaultRequestVoteTimeout
	}
	if c.MaxEntriesPerRPC == 0 {
		c.MaxEntriesPerRPC = defaultMaxEntriesPerRPC
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
