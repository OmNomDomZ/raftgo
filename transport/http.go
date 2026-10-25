package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"raftgo/raft"
)

// HTTP uses internal cluster addresses, never the public redirect addresses.
type HTTP struct {
	Peers  map[raft.NodeID]string
	Client *http.Client
}

func (t *HTTP) call(ctx context.Context, id raft.NodeID, path string, args, reply any) error {
	base, ok := t.Peers[id]
	if !ok {
		return fmt.Errorf("unknown peer %q", id)
	}
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(reply)
}
func (t *HTTP) RequestVote(ctx context.Context, id raft.NodeID, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	var reply raft.RequestVoteReply
	if err := t.call(ctx, id, "/raft/request-vote", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}
func (t *HTTP) AppendEntries(ctx context.Context, id raft.NodeID, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	var reply raft.AppendEntriesReply
	if err := t.call(ctx, id, "/raft/append-entries", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}
