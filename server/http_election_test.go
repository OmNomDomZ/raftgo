package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/raft"
	"raftgo/storage"
)

// Grants are released by the test; no network or wall-clock election races.
type electionTransport struct {
	grants map[raft.NodeID]chan bool
}

func (tr electionTransport) RequestVote(ctx context.Context, peer raft.NodeID, args *raft.RequestVoteArgs) (*raft.RequestVoteReply, error) {
	select {
	case grant := <-tr.grants[peer]:
		return &raft.RequestVoteReply{Term: args.Term, VoteGranted: grant}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (tr electionTransport) AppendEntries(_ context.Context, _ raft.NodeID, args *raft.AppendEntriesArgs) (*raft.AppendEntriesReply, error) {
	return &raft.AppendEntriesReply{Term: args.Term, Success: true}, nil
}

func TestHTTPElectionRolesAndVotes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := electionTransport{grants: map[raft.NodeID]chan bool{}}
		members := []Member{{ID: "A", APIURL: "http://a", RPCURL: "http://a-rpc"}}
		peers := []raft.NodeID{"B", "C", "D", "E"}
		for _, id := range peers {
			tr.grants[id] = make(chan bool, 1)
			members = append(members, Member{ID: id, APIURL: "http://" + string(id), RPCURL: "http://" + string(id) + "-rpc"})
		}
		node, err := raft.New(raft.Config{
			ID: "A", Peers: peers, Storage: &storage.Memory{}, Transport: tr,
			ApplyCh:            make(chan raft.ApplyMsg, 16),
			ElectionTimeoutMin: time.Hour, ElectionTimeoutMax: 2 * time.Hour,
			HeartbeatInterval: time.Minute, RequestVoteTimeout: 3 * time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		h := NewAPI(node, members).Handler()
		check := func(role string, term raft.Term, leader, voted raft.NodeID, votes int, running bool) {
			t.Helper()
			path := "/status"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			var got struct {
				Node    raft.Status `json:"node"`
				Members []Member    `json:"members"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := raft.ElectionStatus{ID: "A", Role: role, Term: term, LeaderID: leader, VotedFor: voted, Votes: votes, Quorum: 3, Running: running}
			if got.Node.Election() != want || !reflect.DeepEqual(got.Members, members) {
				t.Fatalf("%s: got %+v; want %+v, members %v", path, got, want, members)
			}
			// Stage 2 retains the full schema, including zero-valued fields.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw) != 4 {
				t.Fatalf("unexpected envelope: %s", w.Body.String())
			}
			var entries []json.RawMessage
			var consistency string
			if err := json.Unmarshal(raw["entries"], &entries); err != nil || string(raw["entries"]) == "null" || len(entries) != int(got.Node.LastLogIndex) {
				t.Fatalf("entries disagree with node: %s", w.Body.String())
			}
			if err := json.Unmarshal(raw["consistency"], &consistency); err != nil || consistency != "local" {
				t.Fatalf("missing local consistency: %s", w.Body.String())
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw["node"], &fields); err != nil {
				t.Fatal(err)
			}
			keys := []string{"id", "role", "term", "leader_id", "voted_for", "votes", "quorum", "running", "commit_index", "last_log_index"}
			if len(fields) != len(keys) {
				t.Fatalf("unexpected fields: %v", fields)
			}
			for _, key := range keys {
				if _, ok := fields[key]; !ok {
					t.Fatalf("missing %s", key)
				}
			}
		}
		check("Follower", 0, "", "", 0, false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go node.Run(ctx)
		synctest.Wait()
		check("Follower", 0, "", "", 0, true)

		// Duplicate requests grant the same vote, never a second candidate.
		rpc := RPCHandler(node)
		vote := func(candidate string, granted bool) {
			t.Helper()
			w := httptest.NewRecorder()
			body, err := json.Marshal(raft.RequestVoteArgs{Term: 1, CandidateID: raft.NodeID(candidate)})
			if err != nil {
				t.Fatal(err)
			}
			rpc.ServeHTTP(w, httptest.NewRequest("POST", "/raft/request-vote", bytes.NewReader(body)))
			var reply raft.RequestVoteReply
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || w.Code != 200 || reply.Term != 1 || reply.VoteGranted != granted {
				t.Fatalf("vote %s: %d %s (%v)", candidate, w.Code, w.Body.String(), err)
			}
			check("Follower", 1, "", "B", 0, true)
		}
		vote("B", true)
		vote("B", true)
		vote("C", false)

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		check("Candidate", 2, "", "A", 1, true)
		tr.grants["B"] <- true
		synctest.Wait()
		check("Candidate", 2, "", "A", 2, true)
		// Same-term heartbeat steps down without changing this node's vote.
		if _, err := node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: 2, LeaderID: "C"}); err != nil {
			t.Fatal(err)
		}
		check("Follower", 2, "C", "A", 0, true)
		// A new term clears voted_for; leader_id does not imply a vote.
		if _, err := node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: 3, LeaderID: "C"}); err != nil {
			t.Fatal(err)
		}
		check("Follower", 3, "C", "", 0, true)
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		check("Candidate", 4, "", "A", 1, true)
		tr.grants["B"] <- true
		tr.grants["C"] <- true
		synctest.Wait()
		check("Leader", 4, "A", "A", 0, true)
		cancel()
		synctest.Wait()
		// Role is the last local role, but a stopped node knows no leader.
		check("Leader", 4, "", "A", 0, false)
	})
}

func TestHTTPElectionRouting(t *testing.T) {
	node, err := raft.New(raft.Config{ID: "A", Storage: &storage.Memory{}, Transport: electionTransport{}, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewAPI(node, []Member{{ID: "A"}}).Handler()
	for _, path := range []string{"/status"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatalf("%s %s: %d Allow=%q", method, path, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	for _, path := range []string{"/status?poll=1"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/unknown", "/status/", "/status/child", "/election", "/election/child", "/cluster/status", "/cluster/log/node1", "/", "/ui/", "/ui/index.html", "/ui/election.js", "/ui/election.css"} {
		for _, method := range []string{"GET", "POST"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s: %d", method, path, w.Code)
			}
		}
	}
}
