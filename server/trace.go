package server

import (
	"net/http"
	"sync/atomic"
	"time"
)

var requestSequence atomic.Uint64

type traceResponse struct {
	http.ResponseWriter
	status int
}

func (w *traceResponse) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (a *API) trace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestSequence.Add(1)
		start := time.Now()
		out := &traceResponse{ResponseWriter: w, status: 200}
		a.logger.Debug("http request", "request", id, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(out, r)
		a.logger.Debug("http response", "request", id, "status", out.status, "duration", time.Since(start), "raft", a.Node.Status())
	})
}
