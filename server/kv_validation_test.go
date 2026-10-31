package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
)

func TestKVKeyValidationBothEntrypoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		for _, key := range []string{"", strings.Repeat("x", 1025), strings.Repeat("я", 512) + "x", "\xff"} {
			for _, ui := range []bool{false, true} {
				for _, op := range []struct{ method, suffix, body string }{
					{"GET", "", ""}, {"POST", "", `{"value":"v"}`}, {"PUT", "", `{"value":"v"}`}, {"DELETE", "", ""},
					{"POST", "/cas", `{"expected":"v","value":"w"}`}, {"POST", "/lock", ""}, {"DELETE", "/lock", ""},
				} {
					path := "/kv/" + url.PathEscape(key) + op.suffix
					if ui {
						path = "/cluster/kv/a" + op.suffix + "?key=" + url.QueryEscape(key)
					}
					r := httptest.NewRequest(op.method, path, strings.NewReader(op.body)).WithContext(ctx)
					w := httptest.NewRecorder()
					before := api.Node.Status().LastLogIndex
					if key == "" && !ui {
						// ServeMux returns 404 for a missing path segment. Exercise the
						// handler's empty PathValue guard separately, too.
						api.kvRequest(w, r)
					} else {
						h.ServeHTTP(w, r)
					}
					var result struct{ Error string }
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 400 || result.Error != "key must be valid UTF-8, 1..1024 bytes" || api.Node.Status().LastLogIndex != before {
						t.Fatalf("ui=%v %s%s key bytes=%d: %d %s (%v)", ui, op.method, op.suffix, len(key), w.Code, w.Body, err)
					}
				}
			}
		}
		for _, key := range []string{strings.Repeat("x", 1024), strings.Repeat("я", 512), " "} {
			w := kvRequestAt(h, ctx, "PUT", "/kv/"+url.PathEscape(key), `{"value":"v"}`)
			if w.Code != 200 {
				t.Fatalf("key bytes=%d: %d %s", len(key), w.Code, w.Body)
			}
			api.mu.Lock()
			result := api.store.Get(key, "")
			api.mu.Unlock()
			if result.Code != 200 || result.Value != "v" {
				t.Fatalf("key changed: %+v", result)
			}
		}
	})
}

func TestKVUIKeyValidationBoundaries(t *testing.T) {
	// Use a real mux at the destination to verify decoded PathValue too.
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /kv/{key}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"key": r.PathValue("key")})
	})
	peer := httptest.NewServer(mux)
	defer peer.Close()
	h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
	for _, key := range []string{strings.Repeat("x", 1024), strings.Repeat("я", 512), " "} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PUT", "/cluster/kv/peer?key="+url.QueryEscape(key), strings.NewReader(`{"value":"v"}`)))
		var result struct{ Key string }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Key != key {
			t.Fatalf("key bytes=%d: %d %s (%v)", len(key), w.Code, w.Body, err)
		}
	}
}
