package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"raftgo/raft"
)

// Exercise the common mechanism through both real UI handlers, including their
// different response limits, error messages and Retry-After policies.
func TestUIForwardResponses(t *testing.T) {
	for _, route := range []struct {
		name, method, path string
		limit              int
		retry              bool
	}{
		{"entries", "POST", "/cluster/entries/peer", 64 << 10, false},
		{"kv", "PUT", "/cluster/kv/peer?key=key", 6*MaxCommandBytes + 1024, true},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, code := range []int{201, 301, 302, 303, 308, 409, 423, 503, 504} {
				t.Run(strconv.Itoa(code), func(t *testing.T) {
					var calls atomic.Int32
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						w.Header().Set("Location", "http://forbidden.invalid/")
						w.Header().Set("Retry-After", "2")
						w.Header().Set("Set-Cookie", "private=yes")
						writeJSON(w, code, map[string]int{"index": 7, "term": 3})
					}))
					defer peer.Close()
					h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
					if w.Code != code || calls.Load() != 1 || !strings.Contains(w.Body.String(), `"index":7`) {
						t.Fatalf("%d %s calls=%d", w.Code, w.Body, calls.Load())
					}
					if (w.Header().Get("Retry-After") == "2") != route.retry || w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal(w.Header())
					}
				})
			}
			for _, truncated := range []bool{false, true} {
				t.Run("body/truncated="+strconv.FormatBool(truncated), func(t *testing.T) {
					var calls atomic.Int32
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if truncated {
							w.Header().Set("Content-Length", "100")
							io.WriteString(w, `{`)
							return
						}
						io.WriteString(w, strings.Repeat("x", route.limit+1))
					}))
					defer peer.Close()
					h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
					if calls.Load() != 1 {
						t.Fatal("repeated request", calls.Load())
					}
					if truncated {
						if w.Code != 504 || !strings.Contains(w.Body.String(), "response lost") {
							t.Fatal(w.Code, w.Body)
						}
					} else if w.Code != 200 || w.Body.Len() != route.limit {
						t.Fatal(w.Code, w.Body.Len())
					}
				})
			}
			t.Run("cancel", func(t *testing.T) {
				started := make(chan struct{})
				var calls atomic.Int32
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					io.Copy(io.Discard, r.Body)
					close(started)
					<-r.Context().Done()
				}))
				defer peer.Close()
				h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() { <-started; cancel() }()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)).WithContext(ctx))
				if w.Code != 504 || calls.Load() != 1 {
					t.Fatal(w.Code, calls.Load())
				}
			})
		})
	}
}

func TestUIForwardExactKeyChain(t *testing.T) {
	for _, key := range []string{"雪/a%", ".", ".."} {
		for _, operation := range []struct{ method, suffix, body string }{
			{"GET", "", ""}, {"PUT", "", `{"value":"雪"}`},
			{"POST", "/cas", `{"expected":"雪","value":"水"}`},
			{"POST", "/lock", ""}, {"DELETE", "/lock", ""},
		} {
			t.Run(key+operation.method+operation.suffix, func(t *testing.T) {
				var calls atomic.Int32
				var cycle atomic.Bool
				path := "/kv/" + url.PathEscape(key) + operation.suffix
				if key == "." || key == ".." {
					path = "/kv/" + strings.ReplaceAll(key, ".", "%2E") + operation.suffix
				}
				members := make([]Member, 0, 3)
				for i := 0; i < 3; i++ {
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						raw, _ := io.ReadAll(r.Body)
						if r.URL.EscapedPath() != path || r.Method != operation.method || string(raw) != operation.body || r.Header.Get("X-Lock-Token") != "42" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" {
							t.Errorf("unexpected forward: %s %s %s %v", r.Method, r.URL, raw, r.Header)
						}
						if i < 2 || cycle.Load() {
							w.Header().Set("Location", "http://public-"+strconv.Itoa((i+1)%3)+".invalid"+path)
							w.WriteHeader(307)
						} else {
							writeJSON(w, 200, map[string]int{"index": 7, "term": 3})
						}
					}))
					t.Cleanup(peer.Close)
					members = append(members, Member{ID: raft.NodeID("peer" + strconv.Itoa(i)), APIURL: "http://public-" + strconv.Itoa(i) + ".invalid", InternalAPIURL: peer.URL})
				}
				h := electionAPI(t, members).Handler()
				for _, loop := range []bool{false, true} {
					cycle.Store(loop)
					calls.Store(0)
					r := httptest.NewRequest(operation.method, "/cluster/kv/peer0"+operation.suffix+"?key="+url.QueryEscape(key), strings.NewReader(operation.body))
					r.Header.Set("X-Lock-Token", "42")
					r.Header.Set("Cookie", "secret")
					r.Header.Set("Authorization", "secret")
					r.Header.Set("Idempotency-Key", "secret")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					want := 200
					if loop {
						want = 503
					}
					if w.Code != want || calls.Load() != 3 {
						t.Fatal(w.Code, w.Body, calls.Load())
					}
				}
			})
		}
	}
}

type uiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f uiRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUIForwardSharedDeadlineAndNoReplay(t *testing.T) {
	var deadline time.Time
	var calls, closed int
	client := newUIForwardClient()
	transport := client.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	if transport.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	client.Transport = uiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Fatal("transport can replay buffered body")
		}
		d, ok := r.Context().Deadline()
		if !ok || time.Until(d) > 8*time.Second {
			t.Fatal("missing chain deadline")
		}
		if calls == 1 {
			deadline = d
		} else if !d.Equal(deadline) {
			t.Fatal("deadline restarted on redirect")
		}
		code := 307
		headers := http.Header{"Location": {"http://public.invalid/entries"}}
		if calls == 2 {
			code = 201
		}
		return &http.Response{StatusCode: code, Header: headers, Body: &uiCloseBody{Reader: strings.NewReader(`{}`), closed: &closed}}, nil
	})
	w := httptest.NewRecorder()
	forwardUI(w, httptest.NewRequest("POST", "/", nil), client, uiForwardRequest{
		method: "POST", target: "http://first.invalid/entries", body: []byte(`{"command":"雪"}`),
		headers:   http.Header{"Content-Type": {"application/json"}},
		redirects: map[string]string{"http://public.invalid/entries": "http://second.invalid/entries"}, responseLimit: 64 << 10,
	})
	if w.Code != 201 || calls != 2 || closed != 2 {
		t.Fatal(w.Code, calls, closed)
	}
}

type uiCloseBody struct {
	io.Reader
	closed *int
}

func (b *uiCloseBody) Close() error { *b.closed++; return nil }
