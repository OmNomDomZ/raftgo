package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"raftgo/kv"
	"raftgo/raft"
	"raftgo/storage"
	"raftgo/transport"
)

func TestConsumerReplayAndNoOpBarrier(t *testing.T) {
	memory := &storage.Memory{}
	node, err := raft.New(raft.Config{ID: "n", Storage: memory, Transport: &transport.HTTP{}, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	commands := []kv.Command{{Op: "put", Key: "x", Value: "v"}, {Op: "create", Key: "x", Value: "bad"}, {Op: "put", Key: "x", Value: "updated"}}
	for range 2 {
		api := NewAPI(node, nil)
		ch := make(chan raft.ApplyMsg, 5)
		for i, c := range commands {
			data, _ := json.Marshal(c)
			ch <- raft.ApplyMsg{Index: raft.Index(i + 1), Term: 1, Kind: raft.EntryCommand, Command: append([]byte(kv.Prefix), data...)}
		}
		ch <- raft.ApplyMsg{Index: 4, Term: 2, Kind: raft.EntryNoOp}
		ch <- raft.ApplyMsg{Index: 5, Term: 2, Kind: raft.EntryCommand, Command: []byte("opaque raw command")}
		close(ch)
		api.Consume(context.Background(), ch)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := api.waitApplied(ctx, 5); err != nil {
			t.Fatal(err)
		}
		cancel()
		if api.store.Get("x").Value != "updated" || api.results[2].result.Code != 409 || api.results[3].result.Code != 200 {
			t.Fatal("replay diverged or lost command results")
		}
	}
}
