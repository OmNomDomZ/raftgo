package server

import (
	"context"
	"io"
	"net/http"

	"raftgo/raft"
)

// logUI exposes only configured member API endpoints. It never accepts a URL or
// forwards credentials, cookies, arbitrary paths, or environment proxies.
func (a *API) logUI(mux *http.ServeMux, localStatus http.HandlerFunc) {
	members := make(map[string]string, len(a.Members))
	redirects := make(map[string]string, len(a.Members))
	for _, m := range a.Members {
		members[string(m.ID)] = m.apiEndpoint()
		redirects[m.APIURL+"/entries"] = m.apiEndpoint() + "/entries"
	}
	client := newUIForwardClient()
	mux.HandleFunc("GET /cluster/log/{id}", func(w http.ResponseWriter, r *http.Request) {
		base, ok := members[r.PathValue("id")]
		if !ok {
			problem(w, 404, "unknown member")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if raft.NodeID(r.PathValue("id")) == a.Node.Status().ID {
			localStatus(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), statusTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/status", nil)
		if err != nil {
			problem(w, 503, "log unavailable")
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			problem(w, 503, "log unavailable")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			problem(w, 503, "log unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Stream the diagnostic log: its length is not bounded by the command limit.
		// A timeout/truncated JSON is treated as an unsuccessful observation by UI.
		_, _ = io.Copy(w, resp.Body)
	})
	mux.HandleFunc("POST /cluster/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		base, ok := members[r.PathValue("id")]
		if !ok {
			problem(w, 404, "unknown member")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 6*MaxCommandBytes+128))
		if err != nil {
			problem(w, 400, "request size limit exceeded")
			return
		}
		forwardUI(w, r, client, uiForwardRequest{
			method: http.MethodPost, target: base + "/entries", body: raw,
			headers:   http.Header{"Content-Type": {"application/json"}},
			redirects: redirects, responseLimit: 64 << 10,
			unavailable:  "outcome unknown; request was not retried; retry may duplicate command",
			responseLost: "outcome unknown; response lost; request was not retried",
		})
	})
}
