package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"raftgo/raft"
	"raftgo/storage"
	"raftgo/transport"
)

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func TestHTTPClusterCommitRedirectFailover(t *testing.T) {
	const count = 5
	nodes := make([]*raft.Raft, count)
	apis := make([]*API, count)
	rpc := make([]*httptest.Server, count)
	public := make([]*httptest.Server, count)
	cancels := make([]context.CancelFunc, count)
	done := make([]chan struct{}, count)
	members := make([]Member, count)
	for i := range count {
		rpc[i] = httptest.NewUnstartedServer(nil)
		public[i] = httptest.NewUnstartedServer(nil)
		members[i] = Member{ID: raft.NodeID(fmt.Sprintf("n%d", i)), RPCURL: "http://" + rpc[i].Listener.Addr().String(), APIURL: "http://" + public[i].Listener.Addr().String()}
	}
	client := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for i := range count {
		memory := &storage.Memory{}
		peers := map[raft.NodeID]string{}
		var ids []raft.NodeID
		for j, m := range members {
			if i != j {
				peers[m.ID] = m.RPCURL
				ids = append(ids, m.ID)
			}
		}
		apply := make(chan raft.ApplyMsg, 128)
		var err error
		nodes[i], err = raft.New(raft.Config{ID: members[i].ID, Peers: ids, Storage: memory, Transport: &transport.HTTP{Peers: peers, Client: client}, ApplyCh: apply, ElectionTimeoutMin: 300 * time.Millisecond, ElectionTimeoutMax: 600 * time.Millisecond, HeartbeatInterval: 40 * time.Millisecond, AppendEntriesTimeout: 200 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		apis[i] = NewAPI(nodes[i], members)
		rpc[i].Config.Handler = RPCHandler(nodes[i])
		rpc[i].Start()
		defer rpc[i].Close()
		public[i].Config.Handler = apis[i].Handler()
		public[i].Start()
		defer public[i].Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		done[i] = make(chan struct{})
		go func() {
			defer close(done[i])
			consumerDone := make(chan struct{})
			go func() { defer close(consumerDone); apis[i].Consume(ctx, apply) }()
			nodes[i].Run(ctx)
			<-consumerDone
		}()
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		for _, d := range done {
			<-d
		}
	}()
	leader := -1
	eventually(t, func() bool {
		for i, n := range nodes {
			if n.Status().Role == "Leader" {
				leader = i
				return true
			}
		}
		return false
	})
	follower := (leader + 1) % count
	eventually(t, func() bool { return nodes[follower].Leader() == members[leader].ID })
	post := func(i int) *http.Response {
		t.Helper()
		resp, err := client.Post(members[i].APIURL+"/entries", "application/json", bytes.NewBufferString(`{"command":"SET x 42"}`))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post(follower)
	resp.Body.Close()
	if resp.StatusCode != 307 || resp.Header.Get("Location") != members[leader].APIURL+"/entries" {
		t.Fatalf("redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp = post(leader)
	var result struct {
		Index     raft.Index
		Term      raft.Term
		Committed bool
	}
	err := json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 201 || !result.Committed {
		t.Fatalf("append: %d %+v %v", resp.StatusCode, result, err)
	}
	eventually(t, func() bool {
		for _, n := range nodes {
			s, e := n.InspectLog()
			if s.CommitIndex < result.Index || len(e) < int(result.Index) || e[result.Index-1].Term != result.Term || string(e[result.Index-1].Command) != "SET x 42" {
				return false
			}
		}
		return true
	})

	cancels[leader]()
	<-done[leader]
	old := leader
	leader = -1
	eventually(t, func() bool {
		for i, n := range nodes {
			if i != old && n.Status().Role == "Leader" {
				leader = i
				return true
			}
		}
		return false
	})
	resp = post(leader)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("failover append: %d", resp.StatusCode)
	}
	// With only two of five nodes alive, no write may be reported committed.
	alive := 0
	for i := range count {
		if i != old && i != leader {
			alive++
			if alive > 1 {
				cancels[i]()
				<-done[i]
			}
		}
	}
	resp = post(leader)
	resp.Body.Close()
	if resp.StatusCode == 201 {
		t.Fatal("minority committed a write")
	}
}

func TestAPIValidationAndLocalReads(t *testing.T) {
	memory := &storage.Memory{}
	node, err := raft.New(raft.Config{ID: "n", Storage: memory, Transport: &transport.HTTP{}, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewAPI(node, nil).Handler()
	for _, body := range []string{`{}`, `null`, `{"command":3}`, `{"command":"x","extra":1}`, `{"command":"x"} {}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/entries", bytes.NewBufferString(body)))
		if w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/entries", bytes.NewBufferString(`{"command":"x"}`)))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/status", "/election", "/log"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s: %s", path, w.Body.String())
		}
	}
}
