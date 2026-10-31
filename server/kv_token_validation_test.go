package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"raftgo/kv"
)

func TestKVTokenValidationBothEntrypoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api, _, ctx := stringAPI(t, true)
		h := api.Handler()
		for _, tokens := range [][]string{{"7", "8"}, {"", ""}, {"7,8"}, {"7\r"}, {"7\n"}, {"\xff"}, {strings.Repeat("x", 1025)}, {strings.Repeat("я", 512) + "x"}} {
			for _, ui := range []bool{false, true} {
				for _, op := range []struct{ method, suffix, body string }{
					{"GET", "", ""}, {"POST", "", `{"value":"v"}`}, {"PUT", "", `{"value":"v"}`}, {"DELETE", "", ""},
					{"POST", "/cas", `{"expected":"v","value":"w"}`}, {"POST", "/lock", ""}, {"DELETE", "/lock", ""},
				} {
					path := "/kv/x" + op.suffix
					if ui {
						path = "/cluster/kv/a" + op.suffix + "?key=x"
					}
					r := httptest.NewRequest(op.method, path, strings.NewReader(op.body)).WithContext(ctx)
					for _, token := range tokens {
						r.Header.Add("X-Lock-Token", token)
					}
					before := api.Node.Status().LastLogIndex
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					var result struct{ Error string }
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 400 || result.Error != "expected one lock token, at most 1024 UTF-8 bytes" || api.Node.Status().LastLogIndex != before {
						t.Fatalf("ui=%v %s%s: %d %s (%v)", ui, op.method, op.suffix, w.Code, w.Body, err)
					}
				}
			}
		}
		for _, token := range []string{"", strings.Repeat("x", 1024), strings.Repeat("я", 512), " 007 "} {
			r := httptest.NewRequest("PUT", "/kv/x", strings.NewReader(`{"value":"v"}`)).WithContext(ctx)
			r.Header.Set("X-Lock-Token", token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			code := 409 // An opaque nonempty token on a free key is stale.
			if token == "" {
				code = 200
			}
			atomicHTTPResult(t, api, w, code, true)
			_, entries := api.Node.InspectLog()
			var command kv.Command
			raw := strings.TrimPrefix(string(entries[len(entries)-1].Command), kv.Prefix)
			if err := json.Unmarshal([]byte(raw), &command); err != nil || command.Token != token {
				t.Fatalf("token changed: %+v (%v)", command, err)
			}
		}
	})
}

func TestKVUITokenValidationBoundaries(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"token": r.Header.Get("X-Lock-Token")})
	}))
	defer peer.Close()
	h := electionAPI(t, []Member{{ID: "peer", APIURL: peer.URL}}).Handler()
	for _, token := range []string{"", strings.Repeat("x", 1024), strings.Repeat("я", 512), "007"} {
		r := httptest.NewRequest("PUT", "/cluster/kv/peer?key=x", strings.NewReader(`{"value":"v"}`))
		r.Header.Set("X-Lock-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result struct{ Token string }
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Token != token {
			t.Fatalf("token bytes=%d: %d %s (%v)", len(token), w.Code, w.Body, err)
		}
	}
}
