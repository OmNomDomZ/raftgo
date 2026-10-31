package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"raftgo/client"
	"raftgo/raft"
	"raftgo/server"
	"raftgo/storage"
	"raftgo/transport"
)

func TestTwoClientsCASLockAndLostAppliedResponse(t *testing.T) {
	apply := make(chan raft.ApplyMsg, 64)
	node, err := raft.New(raft.Config{ID: "one", Storage: &storage.Memory{}, Transport: &transport.HTTP{}, ApplyCh: apply, ElectionTimeoutMin: 20 * time.Millisecond, ElectionTimeoutMax: 40 * time.Millisecond, HeartbeatInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	api := server.NewAPI(node, nil)
	done := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() { defer close(done); node.Run(ctx) }()
	go func() { defer close(consumerDone); api.Consume(ctx, apply) }()
	defer func() { cancel(); <-done; <-consumerDone }()
	for node.Status().Role != "Leader" {
		select {
		case <-ctx.Done():
			t.Fatal("no leader")
		case <-time.After(time.Millisecond):
		}
	}
	var drop atomic.Bool
	var dropped atomic.Int32
	handler := api.Handler()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if drop.Swap(false) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			if recorder.Code != 200 {
				t.Errorf("apply failed: %d %s", recorder.Code, recorder.Body.String())
			}
			dropped.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer s.Close()
	a := newClient(t, s.URL, nil)
	b := newClient(t, s.URL, nil)
	for _, key := range []string{"x", ".", "..", "a/b?🌊#%+"} {
		if _, err := a.Create(ctx, key, "", ""); err != nil {
			t.Fatal(err)
		}
		if r, err := b.Get(ctx, key, ""); err != nil || r.Value != "" {
			t.Fatal(r, err)
		}
	}
	if _, err := a.Put(ctx, "x", "old", ""); err != nil {
		t.Fatal(err)
	}
	race := func(f, g func() error, want error) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, call := range []func() error{f, g} {
			go func() { <-start; results <- call() }()
		}
		close(start)
		first, second := <-results, <-results
		if !((first == nil && errors.Is(second, want)) || (second == nil && errors.Is(first, want))) {
			t.Fatal(first, second)
		}
	}
	before := node.Status().LastLogIndex
	race(func() error { _, e := a.CAS(ctx, "x", "old", "a", ""); return e }, func() error { _, e := b.CAS(ctx, "x", "old", "b", ""); return e }, client.ErrConflict)
	if node.Status().LastLogIndex != before+2 {
		t.Fatal("CAS was retried or split into multiple commands")
	}
	value, err := a.Get(ctx, "x", "")
	if err != nil || (value.Value != "a" && value.Value != "b") {
		t.Fatal(value, err)
	}
	tokens := make(chan string, 2)
	lock := func(c *client.Client) func() error {
		return func() error {
			r, e := c.Lock(ctx, "missing")
			if e == nil {
				tokens <- r.Token
			}
			return e
		}
	}
	before = node.Status().LastLogIndex
	race(lock(a), lock(b), client.ErrLocked)
	token := <-tokens
	if token == "" || node.Status().LastLogIndex != before+2 {
		t.Fatal("lock missing token or repeated")
	}
	if _, err := b.Put(ctx, "missing", "intruder", ""); !errors.Is(err, client.ErrLocked) {
		t.Fatal(err)
	}
	if _, err := a.Create(ctx, "missing", "owner", token); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CAS(ctx, "missing", "owner", "new", token); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Get(ctx, "missing", token); err != nil || r.Value != "new" {
		t.Fatal(r, err)
	}
	if _, err := a.Delete(ctx, "missing", token); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(ctx, "missing", "intruder", ""); !errors.Is(err, client.ErrLocked) {
		t.Fatal(err)
	}
	if _, err := a.Unlock(ctx, "missing", token); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(ctx, "missing", "stale", token); !errors.Is(err, client.ErrConflict) {
		t.Fatal(err)
	}
	before = node.Status().LastLogIndex
	drop.Store(true)
	if _, err := a.CAS(ctx, "x", value.Value, "applied", ""); !errors.Is(err, client.ErrUnknownOutcome) {
		t.Fatal(err)
	}
	if node.Status().LastLogIndex != before+1 || dropped.Load() != 1 {
		t.Fatal("lost-response mutation retried")
	}
	if r, err := b.Get(ctx, "x", ""); err != nil || r.Value != "applied" {
		t.Fatal(r, err)
	}
}
