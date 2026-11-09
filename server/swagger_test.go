package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSwaggerOfflineRoutes(t *testing.T) {
	mux := http.NewServeMux()
	swaggerUI(mux)
	for _, path := range []string{"/swagger/", "/swagger/init.js", "/swagger/swagger-ui-bundle.js", "/swagger/swagger-ui.css", "/swagger/openapi.json"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s: %d, length %d", path, w.Code, w.Body.Len())
		}
		if path == "/swagger/openapi.json" {
			var doc struct {
				OpenAPI string                     `json:"openapi"`
				Paths   map[string]json.RawMessage `json:"paths"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if _, ok := doc.Paths["/election"]; ok {
				t.Fatal("removed /election route is documented")
			}
			for _, route := range []string{"/status", "/cluster/status"} {
				if _, ok := doc.Paths[route]; !ok {
					t.Fatalf("missing %s", route)
				}
			}
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/swagger", nil))
	if w.Code != 307 || w.Header().Get("Location") != "/swagger/" {
		t.Fatal(w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/swagger/missing", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
