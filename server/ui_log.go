package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"raftgo/raft"
)

// logUI exposes only configured public endpoints. It never accepts a URL or
// forwards credentials, cookies, arbitrary paths, or environment proxies.
func (a *API) logUI(mux *http.ServeMux, localStatus http.HandlerFunc) {
	members := make(map[string]string, len(a.Members))
	for _, m := range a.Members {
		members[string(m.ID)] = m.APIURL
	}
	client := &http.Client{
		Transport:     &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
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
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		w.Header().Set("Cache-Control", "no-store")
		visited := make(map[string]bool)
		target := base + "/entries"
		for !visited[target] {
			visited[target] = true
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
			if err != nil {
				problem(w, 503, "invalid configured endpoint")
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				// Delivery may have succeeded. Never repeat a POST after a lost response.
				problem(w, 504, "outcome unknown; request was not retried; retry may duplicate command")
				return
			}
			if resp.StatusCode != http.StatusTemporaryRedirect {
				data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				if err != nil {
					problem(w, 504, "outcome unknown; response lost; request was not retried")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(data)
				return
			}
			location := resp.Header.Get("Location")
			resp.Body.Close()
			allowed := false
			for _, endpoint := range members {
				if location == endpoint+"/entries" {
					allowed = true
					break
				}
			}
			if !allowed {
				problem(w, 503, "leader is not a configured member")
				return
			}
			// Only an explicit 307 (the node rejected this write as follower) permits
			// forwarding. No discovery-and-retry after 503, 504, or a network failure.
			target = location
		}
		problem(w, 503, "leader redirect cycle")
	})
}
