package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// kvUI forwards CRUD/CAS/locks only to fixed members. Public redirects are matched exactly
// and mapped to internal endpoints; credentials and arbitrary paths never pass.
func (a *API) kvUI(mux *http.ServeMux) {
	members := append([]Member(nil), a.Members...)
	client := &http.Client{
		Transport:     &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, route := range []string{"GET /cluster/kv/{id}", "POST /cluster/kv/{id}", "PUT /cluster/kv/{id}", "DELETE /cluster/kv/{id}", "POST /cluster/kv/{id}/cas", "POST /cluster/kv/{id}/lock", "DELETE /cluster/kv/{id}/lock"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.WriteHeader(405)
				return
			}
			key := r.URL.Query().Get("key")
			if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
				problem(w, 400, "key must be valid UTF-8, 1..1024 bytes")
				return
			}
			// Escape dot-only keys too: ServeMux canonicalizes literal dot segments.
			escaped := url.PathEscape(key)
			if key == "." || key == ".." {
				escaped = strings.ReplaceAll(key, ".", "%2E")
			}
			path := "/kv/" + escaped
			if strings.HasSuffix(r.Pattern, "/cas") {
				path += "/cas"
			} else if strings.HasSuffix(r.Pattern, "/lock") {
				path += "/lock"
			}
			token := r.Header.Get("X-Lock-Token")
			if len(r.Header.Values("X-Lock-Token")) > 1 || len(token) > 1024 || !utf8.ValidString(token) || strings.ContainsAny(token, ",\r\n") {
				problem(w, 400, "expected one lock token, at most 1024 UTF-8 bytes")
				return
			}
			target := ""
			redirects := make(map[string]string)
			for _, m := range members {
				redirects[m.APIURL+path] = m.apiEndpoint() + path
				if string(m.ID) == r.PathValue("id") {
					target = m.apiEndpoint() + path
				}
			}
			if target == "" {
				problem(w, 404, "unknown member")
				return
			}
			var raw []byte
			if r.Method != "GET" {
				var err error
				raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 6*MaxCommandBytes+128))
				if err != nil {
					problem(w, 400, "request size limit exceeded")
					return
				}
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			w.Header().Set("Cache-Control", "no-store")
			visited := make(map[string]bool)
			for !visited[target] {
				visited[target] = true
				req, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(raw))
				if err != nil {
					problem(w, 503, "invalid configured endpoint")
					return
				}
				// Prevent transport replay even though the body is buffered for 307.
				req.GetBody = nil
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("X-Lock-Token", token)
				}
				resp, err := client.Do(req)
				if err != nil {
					problem(w, 504, "response unavailable; outcome unknown for writes; request was not retried")
					return
				}
				if resp.StatusCode != 307 {
					data, err := io.ReadAll(io.LimitReader(resp.Body, 6*MaxCommandBytes+1024))
					resp.Body.Close()
					if err != nil {
						problem(w, 504, "response lost; outcome unknown for writes; request was not retried")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if retry := resp.Header.Get("Retry-After"); retry != "" {
						w.Header().Set("Retry-After", retry)
					}
					w.WriteHeader(resp.StatusCode)
					_, _ = w.Write(data)
					return
				}
				location := resp.Header.Get("Location")
				resp.Body.Close()
				next, ok := redirects[location]
				if !ok {
					problem(w, 503, "leader is not a configured member")
					return
				}
				target = next
			}
			problem(w, 503, "leader redirect cycle")
		})
	}
}
