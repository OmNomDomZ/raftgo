package server

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"raftgo/raft"
)

// These resources depend only on the election schema. In lab1 use the
// election-only Status directly instead of Status.Election(), retaining no log code.
//
//go:embed ui/*
var electionAssets embed.FS

const statusTimeout = 600 * time.Millisecond

type memberObservation struct {
	ID         raft.NodeID          `json:"id"`
	Available  bool                 `json:"available"`
	ObservedAt *time.Time           `json:"observed_at,omitempty"`
	Node       *raft.ElectionStatus `json:"node,omitempty"`
	Error      string               `json:"error,omitempty"`
}

func (a *API) electionUI(mux *http.ServeMux) {
	for path, asset := range map[string]string{"/{$}": "index.html", "/ui/election.js": "election.js", "/ui/election.css": "election.css"} {
		data, err := electionAssets.ReadFile("ui/" + asset)
		if err != nil {
			panic(err)
		}
		contentType := map[string]string{"index.html": "text/html; charset=utf-8", "election.js": "text/javascript; charset=utf-8", "election.css": "text/css; charset=utf-8"}[asset]
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
			_, _ = w.Write(data)
		})
	}
	// Capture the configured membership, never accepting target URLs from requests
	// or from remote status payloads. Redirects and environment proxies are disabled.
	members := append([]Member(nil), a.Members...)
	client := &http.Client{
		Timeout:       statusTimeout,
		Transport:     &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	mux.HandleFunc("GET /cluster/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), statusTimeout)
		defer cancel()
		out := make([]memberObservation, len(members))
		var wg sync.WaitGroup
		for i, m := range members {
			wg.Go(func() {
				out[i] = memberObservation{ID: m.ID, Error: "status unavailable"}
				var status raft.ElectionStatus
				if m.ID == a.Node.Status().ID {
					status = a.Node.Status().Election()
				} else {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiEndpoint()+"/status", nil)
					if err != nil {
						return
					}
					resp, err := client.Do(req)
					if err != nil {
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						return
					}
					// /status writes node before its unbounded entries array. Read
					// only the election projection, with a bounded prefix and timeout.
					observed, ok := readElectionNode(resp.Body)
					if !ok || observed.ID != m.ID {
						return
					}
					status = *observed
				}
				now := time.Now().UTC()
				out[i] = memberObservation{ID: m.ID, Available: true, ObservedAt: &now, Node: &status}
			})
		}
		wg.Wait()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, struct {
			Atomic  bool                `json:"atomic"`
			Members []memberObservation `json:"members"`
		}{false, out})
	})
}

// readElectionNode validates the JSON prefix through node, not the unused log.
func readElectionNode(r io.Reader) (*raft.ElectionStatus, bool) {
	d := json.NewDecoder(io.LimitReader(r, 64<<10))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, false
		}
		if key == "node" {
			var node *raft.ElectionStatus
			if err := d.Decode(&node); err != nil || node == nil {
				return nil, false
			}
			return node, true
		}
		var ignored json.RawMessage
		if err := d.Decode(&ignored); err != nil {
			return nil, false
		}
	}
	return nil, false
}
