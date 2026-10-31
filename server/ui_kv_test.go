package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestKVUIForwarding(t *testing.T) {
	var calls atomic.Int32
	var forbidden atomic.Int32
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forbidden.Add(1) }))
	defer outsider.Close()
	value := strings.Repeat("<", 20000)
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.EscapedPath() != "/kv/a%2Fb%25%3F" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Lock-Token") != "" {
			t.Errorf("unexpected request %s %v", r.URL, r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		if (r.Method == "POST" || r.Method == "PUT") && string(raw) != `{"value":"<b>"}` {
			t.Errorf("body %s", raw)
		}
		switch r.Method {
		case "GET":
			writeJSON(w, 200, map[string]string{"value": value})
		case "POST":
			writeJSON(w, 409, map[string]string{"error": "key exists"})
		case "PUT":
			writeJSON(w, 504, map[string]any{"error": "outcome unknown", "index": 4, "term": 2})
		case "DELETE":
			writeJSON(w, 404, map[string]string{"error": "key absent"})
		}
	}))
	defer leader.Close()
	var location atomic.Value
	location.Store("http://public-leader.invalid/kv/a%2Fb%25%3F")
	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", location.Load().(string))
		w.WriteHeader(307)
	}))
	defer follower.Close()
	h := electionAPI(t, []Member{{ID: "leader", APIURL: "http://public-leader.invalid", InternalAPIURL: leader.URL}, {ID: "follower", APIURL: "http://public-follower.invalid", InternalAPIURL: follower.URL}}).Handler()
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(`{"value":"<b>"}`))
		r.Header.Set("Cookie", "secret=yes")
		r.Header.Set("X-Lock-Token", "secret")
		h.ServeHTTP(w, r)
		return w
	}
	for method, code := range map[string]int{"GET": 200, "POST": 409, "PUT": 504, "DELETE": 404} {
		w := request(method, "/cluster/kv/follower?key=a%2Fb%25%3F&url="+outsider.URL)
		if w.Code != code {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body)
		}
		if method == "GET" && !strings.Contains(w.Body.String(), strings.Repeat(`\u003c`, 20000)) {
			t.Fatal("large escaped value truncated")
		}
		if method == "PUT" && !strings.Contains(w.Body.String(), `"index":4`) {
			t.Fatal("lost timeout metadata")
		}
	}
	for _, target := range []string{outsider.URL + "/kv/a%2Fb%25%3F", "http://public-leader.invalid/kv/other", "http://public-follower.invalid/kv/a%2Fb%25%3F", leader.URL + "/kv/a%2Fb%25%3F"} {
		location.Store(target)
		if w := request("PUT", "/cluster/kv/follower?key=a%2Fb%25%3F"); w.Code != 503 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if calls.Load() != 4 || forbidden.Load() != 0 {
		t.Fatal("unexpected retry or target", calls.Load(), forbidden.Load())
	}
	if w := request("PUT", "/cluster/kv/unknown?key=key"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/cluster/kv/leader/key/cas"); w.Code != 404 {
		t.Fatal("proxy exposes arbitrary paths", w.Code)
	}
}

func TestKVUILostResponseAndUnavailableLeader(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
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
			h.ServeHTTP(w, httptest.NewRequest(method, "/cluster/kv/peer?key=key", strings.NewReader(`{"value":""}`)))
			if w.Code != 504 || calls.Load() != 1 {
				t.Fatal(w.Code, calls.Load())
			}
		})
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		problem(w, 503, "leader unknown")
	}))
	defer peer.Close()
	h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cluster/kv/peer?key=..", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatal(w.Code, w.Header())
	}
}
