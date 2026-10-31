package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/kv"
	"raftgo/raft"
	"raftgo/storage"
)

func kvRequestAt(h http.Handler, ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx))
	return w
}

func TestHTTPKVCRUDStringsAndEscapedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		keys := []string{"x", "ключ 🌊", "a/b?c#d%2F+", ".", "..", strings.Repeat("я", 512)}
		values := []string{"", "Привет 🌊\n\"\\", `{"op":"delete","key":"x"}`}
		for _, key := range keys {
			path := "/kv/" + url.PathEscape(key)
			// ServeMux canonicalizes literal dot segments; encoded segments are keys.
			if key == "." {
				path = "/kv/%2E"
			}
			if key == ".." {
				path = "/kv/%2E%2E"
			}
			call := func(method, value string, want int) kv.Result {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"value": value})
				w := kvRequestAt(h, ctx, method, path, string(body))
				var result kv.Result
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != want || w.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("%s %s: %d %s (%v)", method, path, w.Code, w.Body.String(), err)
				}
				return result
			}
			call("GET", "", 404)
			call("DELETE", "", 404)
			call("POST", "", 201)
			call("POST", "must not replace", 409)
			if got := call("GET", "", 200); got.Value != "" {
				t.Fatal(got)
			}
			for _, value := range values {
				if got := call("PUT", value, 200); got.Value != value || got.Index == 0 {
					t.Fatal(got)
				}
				if got := call("GET", "", 200); got.Value != value {
					t.Fatal(got)
				}
				api.mu.Lock()
				stored := api.store.Get(key)
				api.mu.Unlock()
				if stored.Code != 200 || stored.Value != value {
					t.Fatalf("escaped key changed: %q %+v", key, stored)
				}
			}
			before := api.Node.Status().LastLogIndex
			call("HEAD", "", 200)
			if api.Node.Status().LastLogIndex != before {
				t.Fatal("HEAD proposed a mutation")
			}
			call("DELETE", "", 200)
			call("GET", "", 404)
			call("DELETE", "", 404)
			call("PUT", "upsert", 200)
		}
	})
}

func TestHTTPKVStrictValidationAndSizes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		for _, method := range []string{"POST", "PUT"} {
			for _, body := range []string{"", "{", "[]", "null", `{}`, `{"value":null}`, `{"value":1}`, `{"value":{}}`, `{"value":"x","extra":true}`, `{"Value":"x"}`, `{"value":"a","value":"b"}`, `{"value":"x"} {}`, "{\"value\":\"\xff\"}", strings.Repeat(" ", 6*MaxCommandBytes+129)} {
				before := api.Node.Status().LastLogIndex
				w := kvRequestAt(h, ctx, method, "/kv/x", body)
				if w.Code != 400 || api.Node.Status().LastLogIndex != before {
					t.Fatalf("%s invalid JSON: %d %.150s", method, w.Code, w.Body.String())
				}
			}
		}
		for _, key := range []string{strings.Repeat("x", 1025), strings.Repeat("я", 513), "%FF"} {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				before := api.Node.Status().LastLogIndex
				w := kvRequestAt(h, ctx, method, "/kv/"+key, `{"value":"x"}`)
				if w.Code != 400 || api.Node.Status().LastLogIndex != before {
					t.Fatalf("invalid key: %s %d", method, w.Code)
				}
			}
		}
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			w := kvRequestAt(h, ctx, method, "/kv/", `{"value":"x"}`)
			if w.Code != 404 {
				t.Fatalf("empty path key: %d", w.Code)
			}
		}
		// Limit applies to the complete encoded internal command, prefix included.
		overhead, _ := json.Marshal(kv.Command{Op: "put", Key: "x", Value: "x"})
		n := MaxCommandBytes - len(kv.Prefix) - len(overhead) + 1
		for _, size := range []int{n, n + 1} {
			body, _ := json.Marshal(map[string]string{"value": strings.Repeat("x", size)})
			before := api.Node.Status().LastLogIndex
			w := kvRequestAt(h, ctx, "PUT", "/kv/x", string(body))
			want := 200
			if size > n {
				want = 400
			}
			if w.Code != want {
				t.Fatalf("size %d: %d %s", size, w.Code, w.Body.String())
			}
			if size > n && api.Node.Status().LastLogIndex != before {
				t.Fatal("oversized command proposed")
			}
		}
	})
}

func TestHTTPKVFollowerRedirectAndUnknownLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		s, log := api.Node.InspectLog()
		_, err := api.Node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: s.Term + 1, LeaderID: "b", PrevLogIndex: 1, PrevLogTerm: log[0].Term})
		if err != nil {
			t.Fatal(err)
		}
		h := api.Handler()
		path := "/kv/a%2Fb%3F%25?source=test"
		for _, method := range []string{"GET", "PUT"} {
			w := kvRequestAt(h, ctx, method, path, `{"value":"x"}`)
			if w.Code != 307 || w.Header().Get("Location") != "http://b"+path {
				t.Fatalf("%s redirect: %d %v", method, w.Code, w.Header())
			}
		}
		_, err = api.Node.HandleRequestVote(&raft.RequestVoteArgs{Term: s.Term + 2, CandidateID: "b", LastLogIndex: 1, LastLogTerm: log[0].Term})
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "PUT"} {
			w := kvRequestAt(h, ctx, method, path, `{"value":"x"}`)
			if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
				t.Fatalf("%s: %d %v", method, w.Code, w.Header())
			}
		}
		if api.Node.Status().LastLogIndex != 1 {
			t.Fatal("follower proposed a write")
		}
	})
}

func TestHTTPKVTimeoutDoesNotRetryAndCanApplyLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, tr, ctx := stringAPI(t, true)
		tr.isolated.Store(true)
		w := kvCallLocal(api.Handler(), ctx, "PUT", `{"value":"only once"}`)
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
			t.Fatal("retry or premature commit")
		}
		api.mu.Lock()
		value := api.store.Get("x")
		api.mu.Unlock()
		if value.Code != 404 {
			t.Fatal("uncommitted value applied")
		}
		tr.isolated.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()
		s, log = api.Node.InspectLog()
		if len(log) != 2 || s.CommitIndex != 2 {
			t.Fatal("proposal lost or retried")
		}
		w = kvCallLocal(api.Handler(), ctx, "GET", "")
		var got kv.Result
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Value != "only once" {
			t.Fatalf("late apply: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestHTTPKVSuccessWaitsForApply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ch := make(chan raft.ApplyMsg, 32)
		node, err := raft.New(raft.Config{ID: "a", Storage: &storage.Memory{}, Transport: &applyTestTransport{}, ApplyCh: ch, ElectionTimeoutMin: time.Hour, ElectionTimeoutMax: 2 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go node.Run(ctx)
		synctest.Wait()
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		api := NewAPI(node, nil)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- kvCallLocal(api.Handler(), ctx, "PUT", `{"value":"committed"}`) }()
		synctest.Wait()
		if api.Node.Status().CommitIndex != 2 {
			t.Fatal("not committed")
		}
		select {
		case w := <-done:
			t.Fatalf("answered before apply: %d", w.Code)
		default:
		}
		go api.Consume(ctx, ch)
		synctest.Wait()
		select {
		case w := <-done:
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		default:
			t.Fatal("no response after apply")
		}
	})
}
