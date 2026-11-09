package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"raftgo/raft"
	"raftgo/storage"
)

func forwardingAPI(t *testing.T, members []Member) *API {
	t.Helper()
	node, err := raft.New(raft.Config{ID: "local", Storage: &storage.Memory{}, Transport: electionTransport{}, ApplyCh: make(chan raft.ApplyMsg, 16)})
	if err != nil {
		t.Fatal(err)
	}
	return NewAPI(node, members)
}

func TestEntriesFixedForwarding(t *testing.T) {
	var writes atomic.Int32
	var forbidden atomic.Int32
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
	defer outsider.Close()
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/entries" || string(data) != `{"command":"<b>"}` || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected forward: %s %s %s", r.Method, r.URL, data)
		}
		writeJSON(w, 504, map[string]any{"index": 4, "term": 2, "error": "outcome unknown"})
	}))
	defer leader.Close()
	var location atomic.Value
	location.Store("http://public-leader.invalid/entries")
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", location.Load().(string))
		w.WriteHeader(307)
	}))
	defer follower.Close()
	members := []Member{{ID: "local"}, {ID: "leader", APIURL: "http://public-leader.invalid", InternalAPIURL: leader.URL}, {ID: "follower", APIURL: "http://public-follower.invalid", InternalAPIURL: follower.URL}}
	h := forwardingAPI(t, members).Handler()
	// Изменение исходного списка участников не должно менять разрешённые адреса.
	members[1].APIURL = outsider.URL
	members[1].InternalAPIURL = outsider.URL
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(`{"command":"<b>"}`))
		r.Header.Set("Cookie", "secret=yes")
		h.ServeHTTP(w, r)
		return w
	}
	w := request("POST", "/cluster/entries/follower?url="+outsider.URL)
	if w.Code != 504 || !strings.Contains(w.Body.String(), `"index":4`) || writes.Load() != 1 {
		t.Fatalf("%d %s writes=%d", w.Code, w.Body, writes.Load())
	}
	location.Store(outsider.URL + "/entries")
	if w = request("POST", "/cluster/entries/follower"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	location.Store(leader.URL + "/entries")
	if w = request("POST", "/cluster/entries/follower"); w.Code != 503 {
		t.Fatal("accepted a Location other than the configured public endpoint", w.Code)
	}
	location.Store("http://public-follower.invalid/entries")
	if w = request("POST", "/cluster/entries/follower"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if forbidden.Load() != 0 || writes.Load() != 1 {
		t.Fatal("followed forbidden target or repeated write")
	}
	if w = request("POST", "/cluster/entries/unknown"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/cluster/entries/leader"); w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestEntriesLostWriteResponseIsNotRetried(t *testing.T) {
	var writes atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer peer.Close()
	h := forwardingAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/cluster/entries/peer", strings.NewReader(`{"command":""}`)))
	if w.Code != 504 || writes.Load() != 1 || !strings.Contains(w.Body.String(), "unknown") {
		t.Fatalf("%d %s writes=%d", w.Code, w.Body, writes.Load())
	}
}
