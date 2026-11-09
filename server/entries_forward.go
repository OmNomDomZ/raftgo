package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// entriesForwarding sends writes only to configured members and follows explicit 307 replies.
func (a *API) entriesForwarding(mux *http.ServeMux) {
	members := make(map[string]string, len(a.Members))
	redirects := make(map[string]string, len(a.Members))
	for _, m := range a.Members {
		members[string(m.ID)] = m.apiEndpoint()
		redirects[m.APIURL+"/entries"] = m.apiEndpoint() + "/entries"
	}
	client := &http.Client{
		Transport:     &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
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
			req.GetBody = nil
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
			endpoint, allowed := redirects[location]
			if !allowed {
				problem(w, 503, "leader is not a configured member")
				return
			}
			// Only an explicit 307 (the node rejected this write as follower) permits
			// forwarding. No discovery-and-retry after 503, 504, or a network failure.
			target = endpoint
		}
		problem(w, 503, "leader redirect cycle")
	})
}
