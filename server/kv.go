package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"raftgo/kv"
	"raftgo/raft"
)

type appliedResult struct {
	term   raft.Term
	result kv.Result
}

func (a *API) waitApplied(ctx context.Context, index raft.Index) error {
	for {
		a.mu.Lock()
		applied, changed := a.applied, a.changed
		a.mu.Unlock()
		if applied >= index {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *API) kvRequest(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" || len(key) > 1024 {
		problem(w, 400, "key must be 1..1024 bytes")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	token := r.Header.Get("X-Lock-Token")
	if r.Method == "GET" {
		index, err := a.Node.ReadIndex(ctx)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		if err = a.waitApplied(ctx, index); err != nil {
			problem(w, 504, "read barrier timed out")
			return
		}
		a.mu.Lock()
		result := a.store.Get(key, token)
		a.mu.Unlock()
		writeJSON(w, result.Code, result)
		return
	}
	c := kv.Command{Key: key, Token: token}
	switch {
	case strings.HasSuffix(r.Pattern, "/cas"):
		c.Op = "cas"
	case strings.HasSuffix(r.Pattern, "/lock"):
		if r.Method == "POST" {
			c.Op = "lock"
		} else {
			c.Op = "unlock"
		}
	case r.Method == "POST":
		c.Op = "create"
	case r.Method == "PUT":
		c.Op = "put"
	case r.Method == "DELETE":
		c.Op = "delete"
	}
	if c.Op == "create" || c.Op == "put" || c.Op == "cas" {
		var body struct {
			Value    *string `json:"value"`
			Expected *string `json:"expected,omitempty"`
		}
		if !decode(w, r, 6*MaxCommandBytes+128, &body) {
			return
		}
		if body.Value == nil || (c.Op == "cas" && body.Expected == nil) {
			problem(w, 400, "value and (for CAS) expected are required strings")
			return
		}
		c.Value = *body.Value
		if body.Expected != nil {
			c.Expected = *body.Expected
		}
	}
	data, err := json.Marshal(c)
	if err != nil || len(data)+len(kv.Prefix) > MaxCommandBytes {
		problem(w, 400, "command too large")
		return
	}
	index, term, err := a.Node.Start(append([]byte(kv.Prefix), data...))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	a.logger.Debug("kv proposed", "index", index, "term", term, "op", c.Op)
	if err = a.waitApplied(ctx, index); err != nil {
		writeJSON(w, 504, map[string]any{"error": "commit not confirmed; outcome unknown, retry may execute again", "index": index, "term": term})
		return
	}
	a.mu.Lock()
	result, ok := a.results[index]
	a.mu.Unlock()
	if !ok || result.term != term {
		problem(w, 409, "entry was replaced before commit")
		return
	}
	writeJSON(w, result.result.Code, result.result)
}
