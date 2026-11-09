package raft

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var (
	errNilReply     = errors.New("raft: transport returned a nil reply without an error")
	errInvalidReply = errors.New("raft: invalid reply")
)

// Пишем ошибки RPC в лог; неверные ответы имеют уровень ERROR.
func (rf *Raft) logRPCFailure(rpc string, peer NodeID, term Term, err error) {
	level := slog.LevelDebug
	if errors.Is(err, errInvalidReply) || errors.Is(err, errNilReply) {
		level = slog.LevelError
	}
	if rpc == "AppendEntries" && level == slog.LevelDebug {
		rf.mu.Lock()
		now := rf.clock.Now()
		last, logged := rf.lastAppendFailureLog[peer]
		if logged && now.Sub(last) < 5*time.Second {
			rf.mu.Unlock()
			return
		}
		if rf.lastAppendFailureLog == nil {
			rf.lastAppendFailureLog = make(map[NodeID]time.Time)
		}
		rf.lastAppendFailureLog[peer] = now
		rf.mu.Unlock()
	}
	rf.logger.Log(context.Background(), level, rpc+" failed", "peer", peer, "term", term, "err", err)
}
