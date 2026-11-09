package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"raftgo/raft"
)

const MaxCommandBytes = 64 << 10

type API struct {
	Node    *raft.Raft
	Members []Member
	mu      sync.Mutex
	applied raft.Index
	changed chan struct{}
	logger  *slog.Logger
}

func NewAPI(node *raft.Raft, members []Member) *API {
	return &API{Node: node, Members: members, changed: make(chan struct{}), logger: slog.Default().With("node", node.Status().ID)}
}

// Consume отмечает обработанные записи и будит ожидающие HTTP-запросы.
func (a *API) Consume(ctx context.Context, ch <-chan raft.ApplyMsg) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			a.mu.Lock()
			a.logger.Debug("entry applied", "index", msg.Index, "term", msg.Term, "kind", msg.Kind)
			a.applied = msg.Index
			close(a.changed)
			a.changed = make(chan struct{})
			a.mu.Unlock()
		}
	}
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		problem(w, 400, "expected one JSON object")
		return false
	}
	return true
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	swaggerUI(mux)
	status := func(w http.ResponseWriter, r *http.Request) {
		s, entries := a.Node.InspectLog()
		type entry struct {
			Index     raft.Index `json:"index"`
			Term      raft.Term  `json:"term"`
			Kind      string     `json:"kind"`
			Command   string     `json:"command"`
			Committed bool       `json:"committed"`
		}
		out := make([]entry, len(entries))
		for i, e := range entries {
			kind := "command"
			if e.Kind == raft.EntryNoOp {
				kind = "noop"
			}
			index := raft.Index(i + 1)
			out[i] = entry{index, e.Term, kind, string(e.Command), index <= s.CommitIndex}
		}
		if r.URL.Path == "/log" {
			writeJSON(w, 200, struct {
				Node        raft.Status `json:"node"`
				Consistency string      `json:"consistency"`
				Entries     []entry     `json:"entries"`
			}{s, "local", out})
			return
		}
		writeJSON(w, 200, struct {
			Node        raft.Status `json:"node"`
			Members     []Member    `json:"members"`
			Consistency string      `json:"consistency"`
			Entries     []entry     `json:"entries"`
		}{s, a.Members, "local", out})
	}
	a.entriesForwarding(mux)
	mux.HandleFunc("GET /status", status)
	mux.HandleFunc("GET /log", status)
	mux.HandleFunc("POST /entries", a.appendEntry)
	return a.trace(mux)
}
func (a *API) failure(w http.ResponseWriter, r *http.Request, err error) {
	var notLeader *raft.NotLeaderError
	if errors.As(err, &notLeader) {
		for _, m := range a.Members {
			if m.ID == notLeader.Leader && m.ID != a.Node.Status().ID {
				w.Header().Set("Location", m.APIURL+r.URL.RequestURI())
				problem(w, 307, "send this request to the leader")
				return
			}
		}
		w.Header().Set("Retry-After", "1")
		problem(w, 503, "leader unknown; retry shortly")
		return
	}
	problem(w, 503, err.Error())
}
func (a *API) appendEntry(w http.ResponseWriter, r *http.Request) {
	// Отклоняем неверный UTF-8 до JSON-декодирования, чтобы строка не изменилась незаметно.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 6*MaxCommandBytes+128))
	if err != nil || !utf8.Valid(raw) {
		problem(w, 400, "expected UTF-8 JSON within request size limit")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Command *string `json:"command"`
	}
	if !decode(w, r, 6*MaxCommandBytes+128, &body) {
		return
	}
	if body.Command == nil || len(*body.Command) > MaxCommandBytes {
		problem(w, 400, "command is required and must be at most 65536 bytes")
		return
	}
	index, term, err := a.Node.Start([]byte(*body.Command))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	for {
		a.mu.Lock()
		applied, changed := a.applied, a.changed
		a.mu.Unlock()
		if applied >= index {
			// Проверяем исходный term: на том же индексе уже могла примениться другая запись.
			_, entries := a.Node.InspectLog()
			if index <= raft.Index(len(entries)) && entries[index-1].Term == term {
				writeJSON(w, 201, map[string]any{"index": index, "term": term, "committed": true})
				return
			}
			problem(w, 409, "entry was replaced before commit")
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			writeJSON(w, 504, map[string]any{"error": "commit not confirmed; outcome unknown, retry may duplicate command", "index": index, "term": term})
			return
		}
	}
}

// RPCHandler обслуживает внутренний порт обмена Raft, отдельно от клиентского API.
func RPCHandler(node raft.RPCHandler) http.Handler {
	var heartbeatLogMu sync.Mutex
	var lastHeartbeatLog time.Time
	mux := http.NewServeMux()
	mux.HandleFunc("POST /raft/request-vote", func(w http.ResponseWriter, r *http.Request) {
		var args raft.RequestVoteArgs
		if !decode(w, r, 4096, &args) {
			return
		}
		reply, err := node.HandleRequestVote(&args)
		slog.Debug("request vote RPC", "candidate", args.CandidateID, "term", args.Term, "reply", reply, "error", err)
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		writeJSON(w, 200, reply)
	})
	mux.HandleFunc("POST /raft/append-entries", func(w http.ResponseWriter, r *http.Request) {
		var args raft.AppendEntriesArgs
		if !decode(w, r, 8<<20, &args) {
			return
		}
		reply, err := node.HandleAppendEntries(&args)
		heartbeatLogMu.Lock()
		logAppend := err != nil || len(args.Entries) > 0 || time.Since(lastHeartbeatLog) >= 5*time.Second
		if logAppend {
			lastHeartbeatLog = time.Now()
		}
		heartbeatLogMu.Unlock()
		if logAppend {
			slog.Debug("append entries RPC",
				"leader", args.LeaderID, "term", args.Term,
				"prevLogIndex", args.PrevLogIndex, "prevLogTerm", args.PrevLogTerm,
				"entries", len(args.Entries), "leaderCommit", args.LeaderCommit,
				"reply", reply, "error", err)
		}
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		writeJSON(w, 200, reply)
	})
	return mux
}
