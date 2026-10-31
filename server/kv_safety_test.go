package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"raftgo/kv"
	"raftgo/raft"
	"raftgo/storage"
)

func kvCallLocal(h http.Handler, ctx context.Context, method, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, "/kv/x", strings.NewReader(body)).WithContext(ctx))
	return w
}

// A real applier and a deliberately delayed consumer distinguish commit from
// application. Even a confirmed ReadIndex must not expose the old KV value.
func TestKVReadWaitsForApplication(t *testing.T) {
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
		data, _ := json.Marshal(kv.Command{Op: "put", Key: "x", Value: "committed"})
		index, _, err := node.Start(append([]byte(kv.Prefix), data...))
		if err != nil {
			t.Fatal(err)
		}
		if node.Status().CommitIndex != index {
			t.Fatal("write not committed")
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- kvCallLocal(api.Handler(), ctx, "GET", "") }()
		synctest.Wait()
		select {
		case w := <-done:
			t.Fatalf("read before apply: %d %s", w.Code, w.Body.String())
		default:
		}
		go api.Consume(ctx, ch)
		synctest.Wait()
		select {
		case w := <-done:
			var result kv.Result
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Value != "committed" {
				t.Fatalf("GET: %d %s (%v)", w.Code, w.Body.String(), err)
			}
		default:
			t.Fatal("read did not finish after apply")
		}
	})
}

func TestKVReadCancellationLeadershipAndIsolation(t *testing.T) {
	for _, mode := range []string{"cancel", "higher_term", "no_quorum"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api, tr, ctx := stringAPI(t, true)
				h := api.Handler()
				if w := kvCallLocal(h, ctx, "PUT", `{"value":"old"}`); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				tr.isolated.Store(true)
				readCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- kvCallLocal(h, readCtx, "GET", "") }()
				synctest.Wait()
				select {
				case w := <-done:
					t.Fatalf("isolated leader served GET: %d %s", w.Code, w.Body.String())
				default:
				}
				switch mode {
				case "cancel":
					cancel()
				case "higher_term":
					_, err := api.Node.HandleAppendEntries(&raft.AppendEntriesArgs{Term: api.Node.Status().Term + 1, LeaderID: "b"})
					if err != nil {
						t.Fatal(err)
					}
				case "no_quorum":
					time.Sleep(5 * time.Second)
				}
				synctest.Wait()
				select {
				case w := <-done:
					want := 503
					if w.Code != want {
						t.Fatalf("GET: %d %s, want %d", w.Code, w.Body.String(), want)
					}
				default:
					t.Fatal("read did not resolve")
				}
			})
		})
	}
}

// Each reader begins after its own write completed, while other writers may
// overlap. Values are immutable and use separate keys, so any stale result is
// unambiguously a violation of the read barrier.
func TestKVConcurrentReadsCoverCompletedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		done := make(chan error, 24)
		for i := range 24 {
			go func() {
				path := fmt.Sprintf("/kv/key%d", i)
				for _, method := range []string{"PUT", "GET"} {
					w := httptest.NewRecorder()
					body := fmt.Sprintf(`{"value":"value%d"}`, i)
					h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx))
					var result kv.Result
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Value != fmt.Sprintf("value%d", i) {
						done <- fmt.Errorf("%s %s: %d %s", method, path, w.Code, w.Body.String())
						return
					}
				}
				done <- nil
			}()
		}
		for range 24 {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
}

// Restart the actual Raft node using the same Storage. Persisted entries are
// not evidence of commitment: only a leader's commit boundary permits replay.
func TestKVReplaySameStorageOnlyCommittedPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		memory := &storage.Memory{}
		commands := []kv.Command{{Op: "create", Key: "x", Value: ""}, {Op: "create", Key: "x", Value: "rejected"}, {Op: "put", Key: "x", Value: "Привет 🌊"}, {Op: "delete", Key: "x"}, {Op: "delete", Key: "x"}, {Op: "put", Key: "x", Value: `{"json":"строка"}`}, {Op: "put", Key: "x", Value: "uncommitted"}}
		entries := make([]raft.LogEntry, len(commands))
		for i, c := range commands {
			data, _ := json.Marshal(c)
			entries[i] = raft.LogEntry{Term: 1, Kind: raft.EntryCommand, Command: append([]byte(kv.Prefix), data...)}
		}
		var first map[raft.Index]appliedResult
		for round := range 2 {
			ch := make(chan raft.ApplyMsg, 32)
			node, err := raft.New(raft.Config{ID: "a", Peers: []raft.NodeID{"b", "c"}, Storage: memory, Transport: &applyTestTransport{}, ApplyCh: ch, ElectionTimeoutMin: time.Hour, ElectionTimeoutMax: 2 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			api := NewAPI(node, nil)
			ctx, cancel := context.WithCancel(context.Background())
			stopped := make(chan struct{})
			go func() { defer close(stopped); node.Run(ctx) }()
			go api.Consume(ctx, ch)
			synctest.Wait()
			if api.applied != 0 || api.store.Get("x").Code != 404 {
				t.Fatal("replayed without commit evidence")
			}
			args := &raft.AppendEntriesArgs{Term: 1, LeaderID: "b", LeaderCommit: 6}
			if round == 0 {
				args.Entries = entries
			} else {
				args.PrevLogIndex = 7
				args.PrevLogTerm = 1
			}
			reply, err := node.HandleAppendEntries(args)
			if err != nil || !reply.Success {
				t.Fatalf("append: %v %v", reply, err)
			}
			synctest.Wait()
			if api.applied != 6 || api.store.Get("x").Value != commands[5].Value {
				t.Fatal("lost committed value or applied uncommitted suffix")
			}
			codes := []int{201, 409, 200, 200, 404, 200}
			for i, code := range codes {
				if api.results[raft.Index(i+1)].result.Code != code {
					t.Fatalf("result %d: %+v", i, api.results)
				}
			}
			if round == 0 {
				first = api.results
			} else if !reflect.DeepEqual(first, api.results) {
				t.Fatal("replay changed deterministic results")
			}
			cancel()
			<-stopped
			synctest.Wait()
		}
	})
}

func TestKVEntriesCannotInjectStateMachineCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		if w := kvCallLocal(h, ctx, "PUT", `{"value":"safe"}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		command := kv.Prefix + `{"op":"put","key":"x","value":"injected"}`
		encoded, _ := json.Marshal(map[string]string{"command": command})
		// JSON escapes must be decoded before checking the reserved prefix.
		escaped := strings.Replace(string(encoded), "raftgo", `\u0072aftgo`, 1)
		for _, body := range []string{string(encoded), escaped} {
			before := api.Node.Status().LastLogIndex
			w := entryRequest(h, body)
			if w.Code != 400 || api.Node.Status().LastLogIndex != before {
				t.Fatalf("injection: %d %s", w.Code, w.Body.String())
			}
		}
		// A JSON-looking raw string is still an opaque entry, not a KV command.
		raw, _ := json.Marshal(map[string]string{"command": `{"op":"put","key":"x","value":"injected"}`})
		if w := entryRequest(h, string(raw)); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
		w := kvCallLocal(h, ctx, "GET", "")
		var result kv.Result
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Value != "safe" {
			t.Fatalf("KV changed via /entries: %d %s", w.Code, w.Body.String())
		}
	})
}
