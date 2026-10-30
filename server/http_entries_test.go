package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/raft"
	"raftgo/storage"
)

func stringAPI(t *testing.T, consume bool) (*API, *applyTestTransport, context.Context) {
	t.Helper()
	tr := &applyTestTransport{}
	ch := make(chan raft.ApplyMsg, 32)
	node, err := raft.New(raft.Config{ID: "a", Peers: []raft.NodeID{"b", "c"}, Storage: &storage.Memory{}, Transport: tr, ApplyCh: ch, ElectionTimeoutMin: time.Hour, ElectionTimeoutMax: 2 * time.Hour, HeartbeatInterval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	api := NewAPI(node, []Member{{ID: "a", APIURL: "http://a"}, {ID: "b", APIURL: "http://b"}})
	go node.Run(ctx)
	if consume {
		go api.Consume(ctx, ch)
	}
	synctest.Wait()
	time.Sleep(2 * time.Hour)
	synctest.Wait()
	if node.Status().Role != "Leader" {
		t.Fatal("no leader")
	}
	return api, tr, ctx
}

func entryRequest(h http.Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/entries", strings.NewReader(body)))
	return w
}

func TestHTTPEntriesStringsAndValidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, _ := stringAPI(t, true)
		h := api.Handler()
		// The size limit counts decoded UTF-8 bytes, not JSON wire bytes.
		commands := []string{"", "Привет 🌊\n\"\\", "same", "same", strings.Repeat("x", MaxCommandBytes), strings.Repeat("я", MaxCommandBytes/2), strings.Repeat("\x00", MaxCommandBytes)}
		for i, command := range commands {
			body, _ := json.Marshal(map[string]string{"command": command})
			w := entryRequest(h, string(body))
			var result struct {
				Index     raft.Index
				Term      raft.Term
				Committed bool
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 201 || result.Index != raft.Index(i+2) || !result.Committed || result.Term != api.Node.Status().Term {
				t.Fatalf("command %d: %d %s (%v)", i, w.Code, w.Body.String(), err)
			}
			_, log := api.Node.InspectLog()
			if string(log[result.Index-1].Command) != command {
				t.Fatalf("command %d changed", i)
			}
		}
		oversized, _ := json.Marshal(map[string]string{"command": strings.Repeat("я", MaxCommandBytes/2) + "x"})
		for _, body := range []string{"", "{", "[]", "null", `{}`, `{"command":null}`, `{"command":1}`, `{"command":"x","extra":true}`, `{"command":"x"} {}`, `{"command":"raftgo:kv:v1:{}"}`, "{\"command\":\"\xff\"}", string(oversized), strings.Repeat(" ", 6*MaxCommandBytes+129)} {
			before, _ := api.Node.InspectLog()
			w := entryRequest(h, body)
			after, _ := api.Node.InspectLog()
			if w.Code != 400 || before.LastLogIndex != after.LastLogIndex {
				t.Fatalf("invalid body: code %d, log %d -> %d", w.Code, before.LastLogIndex, after.LastLogIndex)
			}
		}
	})
}

func TestHTTPEntriesTimeoutDoesNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, tr, _ := stringAPI(t, true)
		tr.isolated.Store(true)
		w := entryRequest(api.Handler(), `{"command":"only once"}`)
		var result struct {
			Index raft.Index
			Term  raft.Term
			Error string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 504 || result.Index != 2 || result.Term == 0 || !strings.Contains(result.Error, "outcome unknown") {
			t.Fatalf("%d %s (%v)", w.Code, w.Body.String(), err)
		}
		s, log := api.Node.InspectLog()
		if len(log) != 2 || s.CommitIndex != 1 {
			t.Fatalf("retry or premature commit: %+v %+v", s, log)
		}
		// The same proposal can still commit after the timeout, without a second POST.
		tr.isolated.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()
		s, log = api.Node.InspectLog()
		if len(log) != 2 || s.CommitIndex != 2 {
			t.Fatalf("unknown outcome did not commit: %+v %+v", s, log)
		}
	})
}

func TestHTTPEntriesFollowerRedirectAndUnknownLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, _ := stringAPI(t, true)
		s, log := api.Node.InspectLog()
		if _, err := api.Node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: s.Term + 1, LeaderID: "b", PrevLogIndex: 1, PrevLogTerm: log[0].Term}); err != nil {
			t.Fatal(err)
		}
		w := entryRequest(api.Handler(), `{"command":"x"}`)
		if w.Code != 307 || w.Header().Get("Location") != "http://b/entries" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
		// A higher-term vote clears the known leader.
		if _, err := api.Node.HandleRequestVote(&raft.RequestVoteArgs{Term: s.Term + 2, CandidateID: "b", LastLogIndex: 1, LastLogTerm: log[0].Term}); err != nil {
			t.Fatal(err)
		}
		w = entryRequest(api.Handler(), `{"command":"x"}`)
		if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
		_, log = api.Node.InspectLog()
		if len(log) != 1 {
			t.Fatalf("follower forwarded/retried proposal: %+v", log)
		}
	})
}

func TestHTTPLogSnapshotWithTailAndNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, tr, _ := stringAPI(t, true)
		tr.isolated.Store(true)
		// Add an uncommitted tail directly; diagnostics must neither wait nor redirect.
		if _, _, err := api.Node.Start([]byte("tail")); err != nil {
			t.Fatal(err)
		}
		h := api.Handler()
		var previous string
		for _, path := range []string{"/status", "/election", "/log"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			var result struct {
				Node        raft.Status
				Consistency string
				Entries     []struct {
					Index     raft.Index
					Term      raft.Term
					Kind      string
					Command   string
					Committed bool
				}
				Members []Member
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || result.Consistency != "local" || result.Node.LastLogIndex != 2 || result.Node.CommitIndex != 1 || len(result.Entries) != 2 {
				t.Fatalf("%s: %s", path, w.Body.String())
			}
			for i, e := range result.Entries {
				if e.Index != raft.Index(i+1) || e.Term != result.Node.Term || e.Committed != (e.Index <= result.Node.CommitIndex) {
					t.Fatalf("inconsistent snapshot: %s", w.Body.String())
				}
			}
			if result.Entries[0].Kind != "noop" || result.Entries[0].Command != "" || result.Entries[1].Kind != "command" || result.Entries[1].Command != "tail" {
				t.Fatal(w.Body.String())
			}
			if path != "/log" {
				if len(result.Members) != 2 {
					t.Fatal("missing membership")
				}
				if previous != "" && previous != w.Body.String() {
					t.Fatal("alias differs")
				}
				previous = w.Body.String()
			}
		}
	})
}

func TestClusterStatusWithLargeStringLog(t *testing.T) {
	var payload string
	synctest.Test(t, func(t *testing.T) {
		api, _, _ := stringAPI(t, true)
		body, _ := json.Marshal(map[string]string{"command": strings.Repeat("x", MaxCommandBytes)})
		if w := entryRequest(api.Handler(), string(body)); w.Code != 201 {
			t.Fatal(w.Code)
		}
		// Capture the actual status serialization before using network I/O
		// outside the virtual-time bubble.
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
		if w.Body.Len() <= 64<<10 {
			t.Fatal("test requires a large status")
		}
		payload = w.Body.String()
	})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer remote.Close()
	h := electionAPI(t, []Member{{ID: "a", APIURL: remote.URL}}).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cluster/status", nil))
	var result struct{ Members []memberObservation }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Members) != 1 || !result.Members[0].Available {
		t.Fatalf("large log hid election status: %s (%v)", w.Body.String(), err)
	}
}

func TestElectionNodeProjectionValidation(t *testing.T) {
	for _, body := range []string{`null`, `{`, `{"node":null}`, `{"node":`, `{"node":{"term":"bad"}}`, `{"entries":"` + strings.Repeat("x", 64<<10) + `","node":{"id":"a"}}`} {
		if _, ok := readElectionNode(strings.NewReader(body)); ok {
			t.Fatalf("accepted invalid/oversized prefix: %.80s", body)
		}
	}
}
