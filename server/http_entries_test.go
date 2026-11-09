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
		// Лимит считается по байтам декодированной строки UTF-8, а не по размеру JSON.
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
		for _, body := range []string{"", "{", "[]", "null", `{}`, `{"command":null}`, `{"command":1}`, `{"command":"x","extra":true}`, `{"command":"x"} {}`, "{\"command\":\"\xff\"}", string(oversized), strings.Repeat(" ", 6*MaxCommandBytes+129)} {
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
		// Запись может подтвердиться после таймаута без повторного POST.
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
		if _, _, err := api.Node.Start([]byte("tail")); err != nil {
			t.Fatal(err)
		}
		h := api.Handler()
		for _, path := range []string{"/status", "/log"} {
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
			}
		}
	})
}

func TestStatusWithLargeStringLog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, _ := stringAPI(t, true)
		command := strings.Repeat("x", MaxCommandBytes)
		body, _ := json.Marshal(map[string]string{"command": command})
		if w := entryRequest(api.Handler(), string(body)); w.Code != 201 {
			t.Fatal(w.Code)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
		var result struct {
			Entries []struct{ Command string } `json:"entries"`
		}
		if w.Code != 200 || w.Body.Len() <= 64<<10 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("large status truncated: %d bytes, status %d", w.Body.Len(), w.Code)
		}
		if len(result.Entries) != 2 || result.Entries[1].Command != command {
			t.Fatal("large command changed or missing")
		}
	})
}
