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
	disk, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	node, err := raft.New(raft.Config{ID: "n", Storage: disk, Transport: &transport.HTTP{}, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	commands := []kv.Command{{Op: "put", Key: "x", Value: "v"}, {Op: "lock", Key: "x"}, {Op: "put", Key: "x", Value: "bad"}}
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
		if api.store.Get("x", "").Code != 423 || api.store.Get("x", "2").Value != "v" {
			t.Fatal("replay lost lock or applied rejected write")
		}
		if api.results[3].result.Code != 423 {
			t.Fatal("incorrect command result")
		}
	}
}
