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
		if r.URL.EscapedPath() != "/kv/a%2Fb%25%3F" || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Lock-Token") != "secret" {
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
	for _, operation := range []string{"GET", "POST", "PUT", "DELETE", "POST/cas", "POST/lock", "DELETE/lock"} {
		method, suffix, _ := strings.Cut(operation, "/")
		if suffix != "" {
			suffix = "/" + suffix
		}
		t.Run(operation, func(t *testing.T) {
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
			h.ServeHTTP(w, httptest.NewRequest(method, "/cluster/kv/peer"+suffix+"?key=key", strings.NewReader(`{"value":""}`)))
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

// All owner operations preserve the explicit token across a public 307 while
// never following a redirect that changes operation, key, or membership.
func TestKVUIAtomicForwarding(t *testing.T) {
	for _, op := range []struct{ method, suffix, body string }{
		{"GET", "", ""}, {"POST", "", `{"value":"new"}`},
		{"PUT", "", `{"value":"new"}`}, {"DELETE", "", ""},
		{"POST", "/cas", `{"expected":"old","value":"new"}`},
		{"POST", "/lock", ""}, {"DELETE", "/lock", ""},
	} {
		t.Run(op.method+op.suffix, func(t *testing.T) {
			var calls atomic.Int32
			path := "/kv/a%2Fb" + op.suffix
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if r.Method != op.method || r.URL.EscapedPath() != path || string(raw) != op.body || r.Header.Get("X-Lock-Token") != "42" || r.Header.Get("Cookie") != "" {
					t.Errorf("wrong forward: %s %s %s %v", r.Method, r.URL, raw, r.Header)
				}
				writeJSON(w, 423, map[string]any{"error": "locked", "index": 9, "term": 2})
			}))
			defer peer.Close()
			var location atomic.Value
			location.Store("http://leader.invalid" + path)
			follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", location.Load().(string))
				w.WriteHeader(307)
			}))
			defer follower.Close()
			h := electionAPI(t, []Member{{ID: "f", APIURL: "http://f.invalid", InternalAPIURL: follower.URL}, {ID: "l", APIURL: "http://leader.invalid", InternalAPIURL: peer.URL}}).Handler()
			request := func(tokens ...string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(op.method, "/cluster/kv/f"+op.suffix+"?key=a%2Fb", strings.NewReader(op.body))
				for _, token := range tokens {
					r.Header.Add("X-Lock-Token", token)
				}
				r.Header.Set("Cookie", "private=yes")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := request("42"); w.Code != 423 || !strings.Contains(w.Body.String(), `"index":9`) {
				t.Fatal(w.Code, w.Body)
			}
			location.Store("http://leader.invalid/kv/other" + op.suffix)
			if w := request("42"); w.Code != 503 {
				t.Fatal(w.Code)
			}
			for _, tokens := range [][]string{{"42", "43"}, {"42,43"}, {strings.Repeat("x", 1025)}} {
				if w := request(tokens...); w.Code != 400 {
					t.Fatal(w.Code)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected retry", calls.Load())
			}
		})
	}
}
