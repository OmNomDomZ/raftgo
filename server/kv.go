package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

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
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
		problem(w, 400, "key must be valid UTF-8, 1..1024 bytes")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	token := r.Header.Get("X-Lock-Token")
	if r.Method == "GET" || r.Method == "HEAD" {
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
		fields, ok := decodeKVBody(w, r, c.Op == "cas")
		if !ok {
			return
		}
		c.Value = fields["value"]
		c.Expected = fields["expected"]
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

// Decode exact field names and reject duplicates so a request has one meaning.
func decodeKVBody(w http.ResponseWriter, r *http.Request, cas bool) (map[string]string, bool) {
	const limit = 6*MaxCommandBytes + 128
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || !utf8.Valid(raw) {
		problem(w, 400, "expected UTF-8 JSON within request size limit")
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		problem(w, 400, "expected one JSON object")
		return nil, false
	}
	fields := make(map[string]string)
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		_, duplicate := fields[name]
		if err != nil || !ok || duplicate || (name != "value" && !(cas && name == "expected")) {
			problem(w, 400, "unknown or duplicate JSON field")
			return nil, false
		}
		var value *string
		if err := d.Decode(&value); err != nil || value == nil {
			problem(w, 400, "JSON fields must be strings")
			return nil, false
		}
		fields[name] = *value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		problem(w, 400, "expected one JSON object")
		return nil, false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		problem(w, 400, "expected one JSON object")
		return nil, false
	}
	_, valueOK := fields["value"]
	_, expectedOK := fields["expected"]
	if !valueOK || (cas && !expectedOK) {
		problem(w, 400, "value and (for CAS) expected are required strings")
		return nil, false
	}
	return fields, true
}
