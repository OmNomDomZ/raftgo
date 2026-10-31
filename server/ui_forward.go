package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

func newUIForwardClient() *http.Client {
	return &http.Client{
		// Proxy is deliberately nil: only configured internal endpoints are used.
		Transport:     &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type uiForwardRequest struct {
	method        string
	target        string
	body          []byte
	headers       http.Header       // Explicit outgoing headers, never the incoming header map.
	redirects     map[string]string // Exact public URL to configured internal endpoint.
	responseLimit int64
	retryAfter    bool
	unavailable   string
	responseLost  string
}

// forwardUI follows only explicit, allowlisted 307 responses. Network failures
// and all other statuses terminate the chain; writes must never be replayed.
func forwardUI(w http.ResponseWriter, r *http.Request, client *http.Client, p uiForwardRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	visited := make(map[string]bool)
	for !visited[p.target] {
		visited[p.target] = true
		req, err := http.NewRequestWithContext(ctx, p.method, p.target, bytes.NewReader(p.body))
		if err != nil {
			problem(w, 503, "invalid configured endpoint")
			return
		}
		// The buffered body is only for explicit 307 forwarding, not transport replay.
		req.GetBody = nil
		req.Header = p.headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			problem(w, 504, p.unavailable)
			return
		}
		if resp.StatusCode != http.StatusTemporaryRedirect {
			data, err := io.ReadAll(io.LimitReader(resp.Body, p.responseLimit))
			resp.Body.Close()
			if err != nil {
				problem(w, 504, p.responseLost)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if p.retryAfter {
				if retry := resp.Header.Get("Retry-After"); retry != "" {
					w.Header().Set("Retry-After", retry)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(data)
			return
		}
		location := resp.Header.Get("Location")
		resp.Body.Close()
		next, allowed := p.redirects[location]
		if !allowed {
			problem(w, 503, "leader is not a configured member")
			return
		}
		p.target = next
	}
	problem(w, 503, "leader redirect cycle")
}
