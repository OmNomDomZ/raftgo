package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// kvUI forwards CRUD/CAS/locks only to fixed members. Public redirects are matched exactly
// and mapped to internal endpoints; credentials and arbitrary paths never pass.
func (a *API) kvUI(mux *http.ServeMux) {
	members := append([]Member(nil), a.Members...)
	client := newUIForwardClient()
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
			headers := http.Header{"Content-Type": {"application/json"}}
			if token != "" {
				headers.Set("X-Lock-Token", token)
			}
			forwardUI(w, r, client, uiForwardRequest{
				method: r.Method, target: target, body: raw, headers: headers,
				redirects: redirects, responseLimit: 6*MaxCommandBytes + 1024,
				retryAfter:   true,
				unavailable:  "response unavailable; outcome unknown for writes; request was not retried",
				responseLost: "response lost; outcome unknown for writes; request was not retried",
			})
		})
	}
}
