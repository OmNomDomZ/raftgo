package raft

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGetState_ReflectsRoleAndTerm(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B")

	if term, isLeader := rf.GetState(); term != 0 || isLeader {
		t.Fatalf("fresh node should start at term 0, not leader; got term=%d isLeader=%v", term, isLeader)
	}

	setRoleAndTerm(rf, Leader, 7, "A")

	if term, isLeader := rf.GetState(); term != 7 || !isLeader {
		t.Fatalf("expected leader of term 7, got term=%d isLeader=%v", term, isLeader)
	}
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing ID", func(c *Config) { c.ID = "" }},
		{"peers contain self", func(c *Config) { c.Peers = append(c.Peers, c.ID) }},
		{"duplicate peers", func(c *Config) { c.Peers = []NodeID{"B", "C", "B"} }},
		{"empty peer ID", func(c *Config) { c.Peers = append(c.Peers, "") }},
		{"missing transport", func(c *Config) { c.Transport = nil }},
		{"missing storage", func(c *Config) { c.Storage = nil }},
		{"negative heartbeat interval", func(c *Config) { c.HeartbeatInterval = -time.Millisecond }},
		{"negative RPC timeout", func(c *Config) { c.RPCTimeout = -time.Millisecond }},
		{"heartbeat not below default election timeout", func(c *Config) {
			c.ElectionTimeoutMin, c.ElectionTimeoutMax = 0, 0
			c.HeartbeatInterval = defaultElectionMin
		}},
		{"default heartbeat not below custom election timeout", func(c *Config) {
			c.ElectionTimeoutMin = defaultHeartbeatInterval
		}},
		{"heartbeat not below custom election timeout", func(c *Config) {
			c.HeartbeatInterval = c.ElectionTimeoutMin
		}},
		{"negative election timeout min", func(c *Config) { c.ElectionTimeoutMin = -time.Millisecond }},
		{"negative election timeout max", func(c *Config) { c.ElectionTimeoutMax = -time.Millisecond }},
		{"empty election timeout range", func(c *Config) { c.ElectionTimeoutMax = c.ElectionTimeoutMin }},
		{"custom min above default max", func(c *Config) {
			c.ElectionTimeoutMin, c.ElectionTimeoutMax = defaultElectionMax, 0
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(&fakeStorage{}, "A", "B")
			tt.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestNew_LoadFailureIsReported(t *testing.T) {
	if _, err := New(testConfig(&fakeStorage{failLoad: true}, "A", "B")); err == nil {
		t.Fatalf("expected New to fail when state cannot be loaded")
	}
}

func TestNew_DoesNotAliasPeers(t *testing.T) {
	peers := []NodeID{"B", "C"}
	rf, err := New(testConfig(&fakeStorage{}, "A", peers...))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	peers[0] = "X"
	if rf.peers[0].id != "B" {
		t.Fatalf("caller's slice mutation leaked into the node: %v", rf.peers)
	}
}

func TestAdoptHigherTerm_NotHigherIsFatal(t *testing.T) {
	rf, _ := newTestNode(t, "A", "B", "C")
	setRoleAndTerm(rf, Follower, 3, "B")

	rf.mu.Lock()
	defer rf.mu.Unlock()
	mustBeFatal(t, func() { rf.adoptHigherTermLocked(3) })
	if rf.votedFor != "B" {
		t.Fatalf("vote in term 3 was cleared: votedFor=%q", rf.votedFor)
	}
}

func TestNew_ElectionTimeoutIsRandomizedWithinBounds(t *testing.T) {
	cfg := testConfig(&fakeStorage{}, "A", "B")
	cfg.electionTimeout = nil
	cfg.ElectionTimeoutMin, cfg.ElectionTimeoutMax = 100*time.Millisecond, 200*time.Millisecond
	cfg.Rand = rand.New(rand.NewPCG(1, 2))
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	seen := map[time.Duration]bool{}
	for range 100 {
		d := rf.electionTimeout()
		if d < cfg.ElectionTimeoutMin || d >= cfg.ElectionTimeoutMax {
			t.Fatalf("election timeout %v outside [%v, %v)", d, cfg.ElectionTimeoutMin, cfg.ElectionTimeoutMax)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatalf("election timeout is not randomized: %v", seen)
	}
}

func TestElectionTimeout_OutOfBoundsIsFatal(t *testing.T) {
	// HeartbeatInterval is only validated against ElectionTimeoutMin, so a
	// scripted timeout outside the bounds would test an unsupported setup.
	for _, d := range []time.Duration{time.Second - 1, 2 * time.Second} {
		t.Run(d.String(), func(t *testing.T) {
			cfg := testConfig(&fakeStorage{}, "A", "B")
			cfg.electionTimeout = fixedTimeout(d)
			rf, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			rf.mu.Lock()
			defer rf.mu.Unlock()
			mustBeFatal(t, rf.resetElectionTimerLocked)
		})
	}
}

func TestNew_DefaultElectionTimeoutBounds(t *testing.T) {
	cfg := testConfig(&fakeStorage{}, "A", "B")
	cfg.electionTimeout = nil
	cfg.ElectionTimeoutMin, cfg.ElectionTimeoutMax = 0, 0
	rf, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for range 100 {
		if d := rf.electionTimeout(); d < defaultElectionMin || d >= defaultElectionMax {
			t.Fatalf("election timeout %v outside default [%v, %v)", d, defaultElectionMin, defaultElectionMax)
		}
	}
}

// TestDefaultOnFatal_ReportsAndExits re-runs itself in a subprocess, since the
// default OnFatal exits the process.
func TestDefaultOnFatal_ReportsAndExits(t *testing.T) {
	const msg = "raft: test invariant"
	if os.Getenv("RAFT_TEST_DEFAULT_ON_FATAL") == "1" {
		cfg := testConfig(&fakeStorage{}, "A", "B")
		cfg.OnFatal = nil
		if err := cfg.validateAndSetDefaults(); err != nil {
			t.Fatalf("validateAndSetDefaults: %v", err)
		}
		cfg.OnFatal(msg)
		return // unreachable unless OnFatal returned; the parent sees exit 0
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestDefaultOnFatal_ReportsAndExits$")
	cmd.Env = append(os.Environ(), "RAFT_TEST_DEFAULT_ON_FATAL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit status 1, got %v", err)
	}
	if !strings.Contains(stderr.String(), msg) {
		t.Fatalf("expected %q on stderr, got %q", msg, stderr.String())
	}
}
