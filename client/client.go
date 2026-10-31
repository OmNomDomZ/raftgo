// Package client provides the Raft KV HTTP API. Mutations are never retried;
// a lost response does not imply rollback. Tokens are passed explicitly.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

var (
	ErrNotFound       = errors.New("key not found")
	ErrConflict       = errors.New("conflict")
	ErrLocked         = errors.New("key locked")
	ErrUnavailable    = errors.New("leader unavailable")
	ErrUnknownOutcome = errors.New("outcome unknown; retry may execute again")
	ErrRedirect       = errors.New("redirect refused or limit exceeded")
)

// Result includes the applied index/term for mutations and the token for Lock.
type Result struct {
	Value string `json:"value"`
	Token string `json:"token,omitempty"`
	Index uint64 `json:"index,omitempty"`
	Term  uint64 `json:"term,omitempty"`
	Error string `json:"error,omitempty"`
}

// HTTPError preserves the server's status and result, including index/term.
// Use errors.Is with ErrNotFound, ErrConflict, ErrLocked or ErrUnavailable.
type HTTPError struct {
	StatusCode int
	Result     Result
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("KV HTTP %d: %s", e.StatusCode, e.Result.Error)
}
func (e *HTTPError) Is(target error) bool {
	switch e.StatusCode {
	case 404:
		return target == ErrNotFound
	case 409:
		return target == ErrConflict
	case 423:
		return target == ErrLocked
	case 503:
		return target == ErrUnavailable
	}
	return false
}

// UnknownOutcomeError means a mutation might have committed. Unwrap preserves
// context cancellation, deadline and transport errors (or the HTTP 504).
type UnknownOutcomeError struct{ Cause error }

func (e *UnknownOutcomeError) Error() string {
	return ErrUnknownOutcome.Error() + ": " + e.Cause.Error()
}
func (e *UnknownOutcomeError) Unwrap() error        { return e.Cause }
func (e *UnknownOutcomeError) Is(target error) bool { return target == ErrUnknownOutcome }

type Options struct {
	// HTTPClient is copied; its Transport/Jar remain shared. CheckRedirect is
	// replaced by the client's bounded 307 handling. Timeout covers all hops.
	// A custom Transport must not retry mutations itself.
	HTTPClient *http.Client
	// MaxRedirects is the number of permitted 307 hops; zero disables them.
	MaxRedirects int
}

// Client is safe for concurrent use if its supplied Transport and Jar are.
type Client struct {
	base         url.URL
	http         http.Client
	maxRedirects int
}

// New accepts an HTTP(S) origin (no path, query, fragment or credentials).
// nil options use http.DefaultClient and allow five 307 redirects.
func New(endpoint string, options *Options) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if !validURL(u) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("expected an HTTP(S) origin")
	}
	c := &Client{base: *u, http: *http.DefaultClient, maxRedirects: 5}
	if options != nil {
		if options.MaxRedirects < 0 {
			return nil, errors.New("negative redirect limit")
		}
		c.maxRedirects = options.MaxRedirects
		if options.HTTPClient != nil {
			c.http = *options.HTTPClient
		}
	}
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}

func validURL(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Opaque == ""
}

func (c *Client) Create(ctx context.Context, key, value, token string) (Result, error) {
	return c.do(ctx, "POST", key, "", token, map[string]string{"value": value})
}
func (c *Client) Get(ctx context.Context, key, token string) (Result, error) {
	return c.do(ctx, "GET", key, "", token, nil)
}
func (c *Client) Put(ctx context.Context, key, value, token string) (Result, error) {
	return c.do(ctx, "PUT", key, "", token, map[string]string{"value": value})
}
func (c *Client) Delete(ctx context.Context, key, token string) (Result, error) {
	return c.do(ctx, "DELETE", key, "", token, nil)
}
func (c *Client) CAS(ctx context.Context, key, expected, value, token string) (Result, error) {
	return c.do(ctx, "POST", key, "/cas", token, map[string]string{"expected": expected, "value": value})
}
func (c *Client) Lock(ctx context.Context, key string) (Result, error) {
	return c.do(ctx, "POST", key, "/lock", "", nil)
}
func (c *Client) Unlock(ctx context.Context, key, token string) (Result, error) {
	return c.do(ctx, "DELETE", key, "/lock", token, nil)
}

func (c *Client) do(ctx context.Context, method, key, suffix, token string, fields map[string]string) (Result, error) {
	var zero Result
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) {
		return zero, errors.New("key must be 1..1024 UTF-8 bytes")
	}
	if len(token) > 1024 || !utf8.ValidString(token) || strings.ContainsAny(token, ",\r\n") {
		return zero, errors.New("invalid lock token")
	}
	for _, value := range fields {
		if !utf8.ValidString(value) {
			return zero, errors.New("value must be UTF-8")
		}
	}
	var body []byte
	if fields != nil {
		body, _ = json.Marshal(fields)
	}
	u := c.base
	escaped := url.PathEscape(key)
	if key == "." || key == ".." {
		escaped = strings.Repeat("%2E", len(key))
	}
	u.RawPath = "/kv/" + escaped + suffix
	u.Path = "/kv/" + key + suffix
	u.RawQuery = ""
	hc := c.http
	if hc.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hc.Timeout)
		defer cancel()
		hc.Timeout = 0
	}
	visited := make(map[string]bool)
	for hops := 0; ; hops++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		visited[u.String()] = true
		// Do not give net/http GetBody or idempotency headers: a transport must
		// not replay a mutation after a broken reused connection.
		var reader io.Reader
		if len(body) > 0 {
			reader = io.NopCloser(bytes.NewReader(body))
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
		if err != nil {
			return zero, err
		}
		if len(body) > 0 {
			req.ContentLength = int64(len(body))
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("X-Lock-Token", token)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return zero, uncertain(method, err)
		}
		if resp.StatusCode == http.StatusTemporaryRedirect {
			next, err := resp.Location()
			resp.Body.Close()
			if err != nil || hops >= c.maxRedirects || !validURL(next) || next.Fragment != "" || visited[next.String()] {
				return zero, ErrRedirect
			}
			u = *next
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		var result Result
		decodeErr := json.Unmarshal(data, &result)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			e := &HTTPError{StatusCode: resp.StatusCode, Result: result}
			if resp.StatusCode == 504 {
				return result, uncertain(method, e)
			}
			return result, e
		}
		if readErr != nil {
			return zero, uncertain(method, readErr)
		}
		if len(data) > 1<<20 {
			return zero, uncertain(method, errors.New("response exceeds 1 MiB"))
		}
		if decodeErr != nil {
			return zero, uncertain(method, decodeErr)
		}
		if string(bytes.TrimSpace(data)) == "null" {
			return zero, uncertain(method, errors.New("expected JSON result"))
		}
		return result, nil
	}
}

func uncertain(method string, err error) error {
	if method != "GET" {
		return &UnknownOutcomeError{Cause: err}
	}
	return err
}
