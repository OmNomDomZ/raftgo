package raft

import (
	"context"
	"errors"
	"log/slog"
)

var (
	errNilReply     = errors.New("raft: transport returned a nil reply without an error")
	errInvalidReply = errors.New("raft: invalid reply")
)

// logRPCFailure logs an outgoing RPC that got no usable reply. A lost
// message is routine, but a reply that breaks the protocol means the peer or
// the transport is buggy.
func (rf *Raft) logRPCFailure(rpc string, peer NodeID, term Term, err error) {
	level := slog.LevelDebug
	if errors.Is(err, errInvalidReply) || errors.Is(err, errNilReply) {
		level = slog.LevelError
	}
	rf.logger.Log(context.Background(), level, rpc+" failed", "peer", peer, "term", term, "err", err)
}
