package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"raftgo/raft"
	"raftgo/storage"
	"raftgo/transport"
)

func TestElectionBuildHasNoFutureRoutes(t *testing.T) {
	node, err := raft.New(raft.Config{ID: "a", Storage: &storage.Memory{}, Transport: &transport.HTTP{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAPI(node, nil).Handler()
	for _, path := range []string{"/log", "/entries", "/kv/x", "/kv/x/cas", "/kv/x/lock"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			if response.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", method, path, response.Code)
			}
		}
	}
}
