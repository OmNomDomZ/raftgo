package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/kv"
	"raftgo/raft"
	"raftgo/storage"
)

type httpAtomicResult struct {
	kv.Result
	Term raft.Term `json:"term"`
}

func atomicHTTP(h http.Handler, ctx context.Context, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if token != "" {
		r.Header.Set("X-Lock-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func atomicHTTPResult(t *testing.T, api *API, w *httptest.ResponseRecorder, code int, mutation bool) httpAtomicResult {
	t.Helper()
	var r httpAtomicResult
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != code {
		t.Fatalf("want %d: %d %s (%v)", code, w.Code, w.Body.String(), err)
	}
	if mutation {
		api.mu.Lock()
		applied, ok := api.results[raft.Index(r.Index)]
		ready := api.applied >= raft.Index(r.Index)
		api.mu.Unlock()
		if r.Index == 0 || r.Term == 0 || !ok || !ready || applied.term != r.Term || applied.result.Code != code || applied.result.Value != r.Value || applied.result.Token != r.Token {
			t.Fatalf("response does not identify its applied result: %+v, %+v", r, applied)
		}
	}
	return r
}

func TestHTTPAtomicRacesAndLockAccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		call := func(method, path, body, token string, code int) httpAtomicResult {
			t.Helper()
			return atomicHTTPResult(t, api, atomicHTTP(h, ctx, method, path, body, token), code, method != "GET" && method != "HEAD")
		}
		path := "/kv/a%2Fb%3F%25"
		call("PUT", path, `{"value":"a"}`, "", 200)
		race := func(path string, bodies [2]string, codes [2]int) []httpAtomicResult {
			t.Helper()
			before := api.Node.Status().LastLogIndex
			done := make(chan *httptest.ResponseRecorder, 2)
			for _, body := range bodies {
				go func() { done <- atomicHTTP(h, ctx, "POST", path, body, "") }()
			}
			counts := map[int]int{}
			var results []httpAtomicResult
			for range 2 {
				w := <-done
				counts[w.Code]++
				results = append(results, atomicHTTPResult(t, api, w, w.Code, true))
			}
			if counts[codes[0]] != 1 || counts[codes[1]] != 1 || results[0].Index == results[1].Index || api.Node.Status().LastLogIndex != before+2 {
				t.Fatalf("race must have one winner and one entry per request: %+v %+v", counts, results)
			}
			return results
		}
		rs := race(path+"/cas", [2]string{`{"expected":"a","value":"b"}`, `{"expected":"a","value":"c"}`}, [2]int{200, 409})
		if rs[0].Value != rs[1].Value {
			t.Fatal("CAS winner differs from conflict value")
		}
		if r := call("GET", path, "", "", 200); r.Value != rs[0].Value {
			t.Fatal(r)
		}
		// Lock a missing key, independently of its value.
		path = "/kv/absent%2Fkey"
		rs = race(path+"/lock", [2]string{"", ""}, [2]int{201, 423})
		token := rs[0].Token
		if token == "" {
			token = rs[1].Token
		}
		for _, r := range rs {
			if r.Token != "" && r.Token != strconv.FormatUint(r.Index, 10) {
				t.Fatal(r)
			}
		}
		for _, foreign := range []string{"", "wrong"} {
			for _, op := range []struct{ method, suffix, body string }{
				{"GET", "", ""}, {"HEAD", "", ""}, {"POST", "", `{"value":"v"}`}, {"PUT", "", `{"value":"v"}`}, {"DELETE", "", ""},
				{"POST", "/cas", `{"expected":"v","value":"w"}`}, {"DELETE", "/lock", ""}, {"POST", "/lock", ""},
			} {
				call(op.method, path+op.suffix, op.body, foreign, 423)
			}
		}
		call("GET", path, "", token, 404)
		call("POST", path, `{"value":"v"}`, token, 201)
		call("POST", path, `{"value":"v"}`, token, 409)
		call("PUT", path, `{"value":"w"}`, token, 200)
		call("POST", path+"/cas", `{"expected":"w","value":"🌊"}`, token, 200)
		if r := call("GET", path, "", token, 200); r.Value != "🌊" {
			t.Fatal(r)
		}
		call("POST", path+"/lock", "", token, 423) // not reentrant
		call("DELETE", path, "", token, 200)
		call("GET", path, "", token, 404)
		call("POST", path, `{"value":"reborn"}`, token, 201)
		call("DELETE", path+"/lock", "", token, 200)
		for _, op := range []struct{ method, suffix, body string }{
			{"GET", "", ""}, {"POST", "", `{"value":"v"}`}, {"PUT", "", `{"value":"v"}`}, {"DELETE", "", ""}, {"POST", "/cas", `{"expected":"reborn","value":"w"}`}, {"DELETE", "/lock", ""},
		} {
			call(op.method, path+op.suffix, op.body, token, 409)
		}
		next := call("POST", path+"/lock", "", "", 201)
		previous, _ := strconv.ParseUint(token, 10, 64)
		if next.Index <= previous {
			t.Fatal("fence did not increase")
		}
		call("DELETE", path+"/lock", "", token, 423)
		call("DELETE", path+"/lock", "", next.Token, 200)
	})
}

func TestHTTPAtomicStrictValidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		check := func(method, path, body string, headers []string, code int) {
			t.Helper()
			before := api.Node.Status().LastLogIndex
			r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
			for _, token := range headers {
				r.Header.Add("X-Lock-Token", token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != code || api.Node.Status().LastLogIndex != before {
				t.Fatalf("%s %s: %d %.150s", method, path, w.Code, w.Body.String())
			}
		}
		for _, body := range []string{"", "{", "[]", "null", `{}`, `{"value":"x"}`, `{"expected":"x"}`, `{"expected":null,"value":"x"}`, `{"expected":1,"value":"x"}`, `{"expected":"x","value":false}`, `{"expected":"x","value":null}`, `{"Expected":"x","value":"x"}`, `{"expected":"x","value":"x","extra":0}`, `{"expected":"x","expected":"y","value":"x"}`, `{"expected":"x","value":"x","value":"y"}`, `{"expected":"x","value":"x"} {}`, "{\"expected\":\"\xff\",\"value\":\"x\"}", strings.Repeat(" ", 6*MaxCommandBytes+129), `{"expected":"` + strings.Repeat("x", MaxCommandBytes) + `","value":"x"}`} {
			check("POST", "/kv/x/cas", body, nil, 400)
		}
		for _, method := range []string{"POST", "DELETE"} {
			for _, body := range []string{`{}`, `{"token":"7"}`, " ", strings.Repeat("x", 1024)} {
				check(method, "/kv/x/lock", body, nil, 400)
			}
		}
		for _, op := range []struct{ method, suffix, body string }{
			{"GET", "", ""}, {"POST", "", `{"value":"x"}`}, {"PUT", "", `{"value":"x"}`}, {"DELETE", "", ""}, {"POST", "/cas", `{"expected":"x","value":"y"}`}, {"POST", "/lock", ""}, {"DELETE", "/lock", ""},
		} {
			for _, tokens := range [][]string{{"7", "8"}, {"7,8"}, {strings.Repeat("x", 1025)}, {"\xff"}} {
				check(op.method, "/kv/x"+op.suffix, op.body, tokens, 400)
			}
			for _, key := range []string{"%FF", strings.Repeat("x", 1025)} {
				check(op.method, "/kv/"+key+op.suffix, op.body, nil, 400)
			}
		}
		for _, suffix := range []string{"/cas", "/lock"} {
			methods := []string{"GET", "HEAD", "PUT", "PATCH", "OPTIONS"}
			if suffix == "/cas" {
				methods = append(methods, "DELETE")
			}
			for _, method := range methods {
				check(method, "/kv/x"+suffix, "", nil, 405)
			}
		}
	})
}

// A failed response write is an unknown outcome to the caller. It must not
// trigger another proposal or undo the already applied lock/CAS/unlock.
type lostAtomicResponse struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *lostAtomicResponse) Write(p []byte) (int, error) { w.writes++; return 0, io.ErrClosedPipe }

func TestHTTPAtomicLostResponseAndNoQuorum(t *testing.T) {
	for _, op := range []string{"cas", "lock", "unlock"} {
		t.Run(op, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api, tr, ctx := stringAPI(t, true)
				h := api.Handler()
				atomicHTTPResult(t, api, atomicHTTP(h, ctx, "PUT", "/kv/x", `{"value":"a"}`, ""), 200, true)
				path, body, method, token, code := "/kv/x/cas", `{"expected":"a","value":"b"}`, "POST", "", 200
				if op == "lock" {
					path, body, code = "/kv/x/lock", "", 201
				}
				if op == "unlock" {
					token = atomicHTTPResult(t, api, atomicHTTP(h, ctx, "POST", "/kv/x/lock", "", ""), 201, true).Token
					path, body, method = "/kv/x/lock", "", "DELETE"
				}
				before := api.Node.Status().LastLogIndex
				tr.isolated.Store(true)
				w := atomicHTTP(h, ctx, method, path, body, token)
				var timeout httpAtomicResult
				if err := json.Unmarshal(w.Body.Bytes(), &timeout); err != nil || w.Code != 504 || raft.Index(timeout.Index) != before+1 || timeout.Term == 0 || !strings.Contains(timeout.Error, "outcome unknown") {
					t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
				}
				api.mu.Lock()
				applied := api.applied
				api.mu.Unlock()
				if applied >= raft.Index(timeout.Index) || api.Node.Status().LastLogIndex != before+1 {
					t.Fatal("unconfirmed operation applied or retried")
				}
				if w := atomicHTTP(h, ctx, "GET", "/kv/x", "", token); w.Code != 503 {
					t.Fatalf("GET without quorum: %d", w.Code)
				}
				tr.isolated.Store(false)
				time.Sleep(time.Minute)
				synctest.Wait()
				api.mu.Lock()
				result := api.results[raft.Index(timeout.Index)]
				api.mu.Unlock()
				if result.term != timeout.Term || result.result.Code != code || api.Node.Status().LastLogIndex != before+1 {
					t.Fatal("timeout did not resolve to original single proposal")
				}
				// Repeat setup for a distinct request whose response cannot be delivered.
				if op == "cas" {
					body = `{"expected":"b","value":"c"}`
				}
				if op == "lock" {
					atomicHTTPResult(t, api, atomicHTTP(h, ctx, "DELETE", path, "", result.result.Token), 200, true)
				}
				if op == "unlock" {
					token = atomicHTTPResult(t, api, atomicHTTP(h, ctx, "POST", path, "", ""), 201, true).Token
				}
				before = api.Node.Status().LastLogIndex
				req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
				req.Header.Set("X-Lock-Token", token)
				lost := &lostAtomicResponse{ResponseRecorder: httptest.NewRecorder()}
				h.ServeHTTP(lost, req)
				synctest.Wait()
				if lost.Code != code || lost.writes != 1 || api.Node.Status().LastLogIndex != before+1 {
					t.Fatalf("response loss caused retry: %+v", lost)
				}
				api.mu.Lock()
				result = api.results[before+1]
				api.mu.Unlock()
				if result.result.Code != code {
					t.Fatal("lost response changed applied outcome")
				}
			})
		})
	}
}

func TestHTTPAtomicReplacedProposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, tr, ctx := stringAPI(t, true)
		h := api.Handler()
		tr.isolated.Store(true)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- atomicHTTP(h, ctx, "POST", "/kv/x/lock", "", "") }()
		synctest.Wait()
		s, log := api.Node.InspectLog()
		if s.LastLogIndex != 2 || s.CommitIndex != 1 {
			t.Fatal(s)
		}
		reply, err := api.Node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: s.Term + 1, LeaderID: "b", PrevLogIndex: 1, PrevLogTerm: log[0].Term, Entries: []raft.LogEntry{{Term: s.Term + 1, Kind: raft.EntryNoOp}}, LeaderCommit: 2})
		if err != nil || !reply.Success {
			t.Fatalf("%+v %v", reply, err)
		}
		synctest.Wait()
		w := <-done
		if w.Code != 409 || !strings.Contains(w.Body.String(), "replaced") {
			t.Fatalf("replaced index reported success: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestHTTPAtomicWaitsForConsumer(t *testing.T) {
	for _, op := range []string{"cas", "lock", "unlock"} {
		t.Run(op, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ch := make(chan raft.ApplyMsg, 16)
				node, err := raft.New(raft.Config{ID: "a", Storage: &storage.Memory{}, Transport: &applyTestTransport{}, ApplyCh: ch, ElectionTimeoutMin: time.Hour, ElectionTimeoutMax: 2 * time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				go node.Run(ctx)
				synctest.Wait()
				time.Sleep(2 * time.Hour)
				synctest.Wait()
				api := NewAPI(node, nil)
				method, path, body, code := "POST", "/kv/x/cas", `{"expected":"a","value":"b"}`, 404
				if op == "lock" {
					path, body, code = "/kv/x/lock", "", 201
				}
				if op == "unlock" {
					method, path, body, code = "DELETE", "/kv/x/lock", "", 409
				}
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- atomicHTTP(api.Handler(), ctx, method, path, body, "") }()
				synctest.Wait()
				if node.Status().CommitIndex != 2 {
					t.Fatal("not committed")
				}
				select {
				case w := <-done:
					t.Fatalf("answered before apply: %d", w.Code)
				default:
				}
				go api.Consume(ctx, ch)
				synctest.Wait()
				atomicHTTPResult(t, api, <-done, code, true)
			})
		})
	}
}
