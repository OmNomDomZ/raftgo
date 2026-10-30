package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLogUIFixedForwarding(t *testing.T) {
	var writes atomic.Int32
	var forbidden atomic.Int32
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
	defer outsider.Close()
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			io.WriteString(w, `{"node":{"id":"leader"},"consistency":"local","entries":[]}`)
			return
		}
		writes.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/entries" || string(data) != `{"command":"<b>"}` || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected forward: %s %s %s", r.Method, r.URL, data)
		}
		writeJSON(w, 504, map[string]any{"index": 4, "term": 2, "error": "outcome unknown"})
	}))
	defer leader.Close()
	var location atomic.Value
	location.Store(leader.URL + "/entries")
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", location.Load().(string))
		w.WriteHeader(307)
	}))
	defer follower.Close()
	members := []Member{{ID: "local"}, {ID: "leader", APIURL: leader.URL}, {ID: "follower", APIURL: follower.URL}}
	h := electionAPI(t, members).Handler()
	// Handler targets are a snapshot, not caller-controlled data.
	members[1].APIURL = outsider.URL
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
	location.Store(follower.URL + "/entries")
	if w = request("POST", "/cluster/entries/follower"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if forbidden.Load() != 0 || writes.Load() != 1 {
		t.Fatal("followed forbidden target or repeated write")
	}
	for path, code := range map[string]int{"/cluster/log/leader": 200, "/cluster/log/local": 200, "/cluster/log/follower": 503, "/cluster/log/unknown": 404} {
		if w = request("GET", path); w.Code != code {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	if w = request("POST", "/cluster/entries/unknown"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/cluster/entries/leader"); w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestLogUILostWriteResponseIsNotRetried(t *testing.T) {
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
	h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/cluster/entries/peer", strings.NewReader(`{"command":""}`)))
	if w.Code != 504 || writes.Load() != 1 || !strings.Contains(w.Body.String(), "unknown") {
		t.Fatalf("%d %s writes=%d", w.Code, w.Body, writes.Load())
	}
}
