package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"raftgo/raft"
	"raftgo/storage"
)

func electionAPI(t *testing.T, members []Member) *API {
	t.Helper()
	n, err := raft.New(raft.Config{ID: "local", Peers: []raft.NodeID{"good", "slow", "redirect", "bad"}, Storage: &storage.Memory{}, Transport: electionTransport{}, ApplyCh: make(chan raft.ApplyMsg, 16)})
	if err != nil {
		t.Fatal(err)
	}
	return NewAPI(n, members)
}

func TestElectionUIRouting(t *testing.T) {
	h := electionAPI(t, nil).Handler()
	for path, content := range map[string]string{"/": "ЭТАП 2", "/ui/election.js": "textContent", "/ui/election.css": "#nodes"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), content) || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for path, code := range map[string]int{"/unknown": 404, "/ui/unknown": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/ui/election.js", "/cluster/status"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 405 {
			t.Fatalf("POST %s: %d", path, w.Code)
		}
	}
}

func TestClusterStatusPartialFixedMembership(t *testing.T) {
	var forbidden atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
	defer target.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Errorf("unexpected path %s", r.URL)
		}
		writeJSON(w, 200, map[string]any{"node": raft.ElectionStatus{ID: "good", Role: "Candidate", Term: 7, VotedFor: "<img src=x onerror=alert(1)>", Votes: 2, Quorum: 3, Running: true}, "members": []Member{{ID: "injected", APIURL: target.URL}}})
	}))
	defer good.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"node": raft.ElectionStatus{ID: "wrong"}})
	}))
	defer bad.Close()
	members := []Member{{ID: "local"}, {ID: "good", APIURL: good.URL}, {ID: "slow", APIURL: slow.URL}, {ID: "redirect", APIURL: redirect.URL}, {ID: "bad", APIURL: bad.URL}}
	h := electionAPI(t, members).Handler()
	// Mutating the caller's membership after constructing Handler cannot change its targets.
	members[1].APIURL = target.URL
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cluster/status?url="+target.URL, nil))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("unbounded poll: %v", elapsed)
	}
	var got struct {
		Atomic  bool                `json:"atomic"`
		Members []memberObservation `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got.Atomic || len(got.Members) != 5 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	for i, m := range got.Members {
		if m.ID != members[i].ID || m.Available != (i < 2) {
			t.Fatalf("member %d: %+v", i, m)
		}
		if m.Available && (m.Node == nil || m.ObservedAt == nil || m.Error != "") {
			t.Fatalf("missing successful observation: %+v", m)
		}
		if !m.Available && (m.Node != nil || m.ObservedAt != nil || m.Error == "") {
			t.Fatalf("failed observation fabricated data: %+v", m)
		}
	}
	if got.Members[1].Node.VotedFor != "<img src=x onerror=alert(1)>" {
		t.Fatal("text changed")
	}
	if forbidden.Load() != 0 {
		t.Fatal("followed injected target or redirect")
	}
	if strings.Contains(w.Body.String(), "commit_index") || strings.Contains(w.Body.String(), "<img") {
		t.Fatal("non-election fields or unescaped HTML")
	}
}
