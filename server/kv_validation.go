package server

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

func validateKVKey(w http.ResponseWriter, key string) bool {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
		problem(w, 400, "key must be valid UTF-8, 1..1024 bytes")
		return false
	}
	return true
}

// Tokens are opaque to HTTP; ownership/staleness is decided by the Store.
// Reject ambiguous headers and bound tokens even on the read path.
func kvLockToken(w http.ResponseWriter, headers http.Header) (string, bool) {
	token := headers.Get("X-Lock-Token")
	if len(headers.Values("X-Lock-Token")) > 1 || len(token) > 1024 || !utf8.ValidString(token) || strings.ContainsAny(token, ",\r\n") {
		problem(w, 400, "expected one lock token, at most 1024 UTF-8 bytes")
		return "", false
	}
	return token, true
}
