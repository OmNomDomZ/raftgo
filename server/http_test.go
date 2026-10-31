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

	kvCall := func(i int, method, path, body, token string, want int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, members[i].APIURL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Lock-Token", token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %v", method, path, resp.StatusCode, want, result)
		}
		return result
	}
	kvCall(follower, "PUT", "/kv/x", `{"value":"a"}`, "", 307)
	kvCall(leader, "POST", "/kv/x", `{"value":"a"}`, "", 201)
	kvCall(leader, "POST", "/kv/x/cas", `{"expected":"wrong","value":"bad"}`, "", 409)
	// Concurrent CAS operations share one expected value: only one may succeed.
	codes := make(chan int, 2)
	for _, value := range []string{"b", "c"} {
		go func(value string) {
			resp, err := client.Post(members[leader].APIURL+"/kv/x/cas", "application/json", bytes.NewBufferString(fmt.Sprintf(`{"expected":"a","value":%q}`, value)))
			if err != nil {
				codes <- 0
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}(value)
	}
	first, second := <-codes, <-codes
	if !((first == 200 && second == 409) || (first == 409 && second == 200)) {
		t.Fatalf("CAS race: %d %d", first, second)
	}
	// Race actual HTTP lock requests on an absent key.
	locks := make(chan map[string]any, 2)
	lockCodes := make(chan int, 2)
	for range 2 {
		go func() {
			resp, err := client.Post(members[leader].APIURL+"/kv/race/lock", "application/json", nil)
			if err != nil {
				lockCodes <- 0
				locks <- nil
				return
			}
			defer resp.Body.Close()
			var result map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&result)
			lockCodes <- resp.StatusCode
			locks <- result
		}()
	}
	first, second = <-lockCodes, <-lockCodes
	if !((first == 201 && second == 423) || (first == 423 && second == 201)) {
		t.Fatalf("lock race: %d %d", first, second)
	}
	for range 2 {
		if r := <-locks; r["token"] != nil {
			kvCall(leader, "DELETE", "/kv/race/lock", "", r["token"].(string), 200)
		}
	}

	lock := kvCall(leader, "POST", "/kv/x/lock", "", "", 201)
	token := lock["token"].(string)
	kvCall(leader, "PUT", "/kv/x", `{"value":"bad"}`, "", 423)
	kvCall(leader, "PUT", "/kv/x", `{"value":"protected"}`, token, 200)
	kvCall(leader, "GET", "/kv/x", "", "", 423)
	if got := kvCall(leader, "GET", "/kv/x", "", token, 200); got["value"] != "protected" {
		t.Fatal(got)
	}
	// Follow a real 307 with each operation, retaining method, body and token.
	redirectClient := &http.Client{Timeout: 7 * time.Second}
	defer redirectClient.CloseIdleConnections()
	follow := func(method, path, body, token string, code int) {
		t.Helper()
		req, err := http.NewRequest(method, members[follower].APIURL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Lock-Token", token)
		resp, err := redirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var r httpAtomicResult
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || resp.StatusCode != code || resp.Request.URL.Host != public[leader].Listener.Addr().String() {
			t.Fatalf("redirect %s: %d %+v %v", method, resp.StatusCode, r, err)
		}
		if method != "GET" && (r.Index == 0 || r.Term == 0) {
			t.Fatal("missing applied index/term")
		}
	}
	follow("GET", "/kv/x", "", token, 200)
	follow("PUT", "/kv/x", `{"value":"via redirect"}`, token, 200)
	follow("POST", "/kv/x/cas", `{"expected":"via redirect","value":"protected"}`, token, 200)
	follow("DELETE", "/kv/x", "", token, 200)
	follow("POST", "/kv/x", `{"value":"protected"}`, token, 201)
	follow("POST", "/kv/x/lock", "", token, 423)
	follow("DELETE", "/kv/x/lock", "", token, 200)
	lock = kvCall(leader, "POST", "/kv/x/lock", "", "", 201)
	token = lock["token"].(string)
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
	kvCall(leader, "PUT", "/kv/x", `{"value":"bad"}`, "", 423)
	if got := kvCall(leader, "GET", "/kv/x", "", token, 200); got["value"] != "protected" {
		t.Fatal(got)
	}
	kvCall(leader, "POST", "/kv/x/cas", `{"expected":"protected","value":"after failover"}`, token, 200)
	kvCall(leader, "DELETE", "/kv/x/lock", "", token, 200)
	nextLock := kvCall(leader, "POST", "/kv/x/lock", "", "", 201)["token"].(string)
	if token == nextLock {
		t.Fatal("lock token reused after failover")
	}
	kvCall(leader, "DELETE", "/kv/x/lock", "", token, 423)
	kvCall(leader, "DELETE", "/kv/x/lock", "", nextLock, 200)
	kvCall(leader, "DELETE", "/kv/x", "", "", 200)
	kvCall(leader, "GET", "/kv/x", "", "", 404)
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

	// A minority cannot confirm a KV write or serve a linearizable read.
	for _, method := range []string{"PUT", "GET"} {
		req, err := http.NewRequest(method, members[leader].APIURL+"/kv/minority", bytes.NewBufferString(`{"value":"uncommitted"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Fatalf("minority served %s: %d", method, resp.StatusCode)
		}
	}
}

func TestAPIValidationAndLocalReads(t *testing.T) {
	memory := &storage.Memory{}
	node, err := raft.New(raft.Config{ID: "n", Storage: memory, Transport: &transport.HTTP{}, ApplyCh: make(chan raft.ApplyMsg)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewAPI(node, nil).Handler()
	for _, body := range []string{`{}`, `null`, `{"command":3}`, `{"command":"x","extra":1}`, `{"command":"x"} {}`, `{"command":"raftgo:kv:v1:{}"}`} {
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
