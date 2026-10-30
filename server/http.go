package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"raftgo/raft"
)

type API struct {
	Node    *raft.Raft
	Members []Member
	logger  *slog.Logger
}

func NewAPI(node *raft.Raft, members []Member) *API {
	return &API{Node: node, Members: members, logger: slog.Default().With("node", node.Status().ID)}
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
	a.electionUI(mux)
	status := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"node": a.Node.Status(), "members": a.Members})
	}
	mux.HandleFunc("GET /status", status)
	mux.HandleFunc("GET /election", status)
	return a.trace(mux)
}

// RPCHandler belongs on a private listener, separate from the public API.
func RPCHandler(node raft.RPCHandler) http.Handler {
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
		if !decode(w, r, 4096, &args) {
			return
		}
		reply, err := node.HandleAppendEntries(&args)
		slog.Debug("append entries RPC", "leader", args.LeaderID, "term", args.Term, "reply", reply, "error", err)
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		writeJSON(w, 200, reply)
	})
	return mux
}
