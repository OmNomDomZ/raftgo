package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"raftgo/client"
)

func newClient(t *testing.T, endpoint string, options *client.Options) *client.Client {
	t.Helper()
	c, err := client.New(endpoint, options)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRedirectMethodsBodiesTokens(t *testing.T) {
	for _, op := range []string{"create", "get", "put", "delete", "cas", "lock", "unlock"} {
		t.Run(op, func(t *testing.T) {
			var calls atomic.Int32
			key := "a/b?ключ#%+"
			leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				method := "POST"
				suffix := ""
				token := "fence"
				wantBody := ""
				switch op {
				case "get":
					method = "GET"
				case "put":
					method = "PUT"
				case "delete":
					method = "DELETE"
				case "cas":
					suffix = "/cas"
				case "lock":
					suffix = "/lock"
					token = ""
				case "unlock":
					method = "DELETE"
					suffix = "/lock"
				}
				switch op {
				case "create", "put":
					wantBody = `{"value":"新\n"}`
				case "cas":
					wantBody = `{"expected":"old","value":"新\n"}`
				}
				if r.Method != method || r.URL.Path != "/kv/"+key+suffix || r.Header.Get("X-Lock-Token") != token || string(body) != wantBody {
					t.Errorf("%s %s %q token=%q", r.Method, r.URL.Path, body, r.Header.Get("X-Lock-Token"))
				}
				io.WriteString(w, `{"value":"新\n","index":7,"term":2,"token":"7"}`)
			}))
			defer leader.Close()
			follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", leader.URL+r.URL.RequestURI())
				w.WriteHeader(307)
			}))
			defer follower.Close()
			hc := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { t.Error("caller redirect callback used"); return nil }}
			c := newClient(t, follower.URL, &client.Options{HTTPClient: hc, MaxRedirects: 2})
			ctx := context.Background()
			var result client.Result
			var err error
			switch op {
			case "create":
				result, err = c.Create(ctx, key, "新\n", "fence")
			case "get":
				result, err = c.Get(ctx, key, "fence")
			case "put":
				result, err = c.Put(ctx, key, "新\n", "fence")
			case "delete":
				result, err = c.Delete(ctx, key, "fence")
			case "cas":
				result, err = c.CAS(ctx, key, "old", "新\n", "fence")
			case "lock":
				result, err = c.Lock(ctx, key)
			case "unlock":
				result, err = c.Unlock(ctx, key, "fence")
			}
			if err != nil || result.Index != 7 || result.Term != 2 || result.Token != "7" || calls.Load() != 1 {
				t.Fatal(result, err, calls.Load())
			}
		})
	}
}

func TestRedirectLimitsAndOtherStatuses(t *testing.T) {
	for _, status := range []int{301, 302, 303, 308, 307} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/kv/next")
				w.WriteHeader(status)
			}))
			defer s.Close()
			c := newClient(t, s.URL, &client.Options{MaxRedirects: 1})
			_, err := c.Put(context.Background(), "x", "v", "")
			if err == nil || calls.Load() != 1 && status != 307 || status == 307 && (!errors.Is(err, client.ErrRedirect) || calls.Load() != 2) {
				t.Fatal(err, calls.Load())
			}
		})
	}
	t.Run("cycle", func(t *testing.T) {
		var calls atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Location", r.URL.String())
			w.WriteHeader(307)
		}))
		defer s.Close()
		c := newClient(t, s.URL, nil)
		_, err := c.Lock(context.Background(), "x")
		if !errors.Is(err, client.ErrRedirect) || calls.Load() != 1 {
			t.Fatal(err, calls.Load())
		}
	})
}

func TestTypedErrors(t *testing.T) {
	for code, target := range map[int]error{404: client.ErrNotFound, 409: client.ErrConflict, 423: client.ErrLocked, 503: client.ErrUnavailable, 504: client.ErrUnknownOutcome} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, `{"error":"reason","index":9,"term":3}`)
		}))
		c := newClient(t, s.URL, nil)
		r, err := c.CAS(context.Background(), "x", "a", "b", "")
		var he *client.HTTPError
		if !errors.Is(err, target) || !errors.As(err, &he) || he.StatusCode != code || r.Index != 9 || he.Result.Term != 3 {
			t.Fatal(r, err)
		}
		if code == 504 {
			_, err = c.Get(context.Background(), "x", "")
			if errors.Is(err, client.ErrUnknownOutcome) {
				t.Fatal("read marked as unknown mutation")
			}
		}
		s.Close()
	}
}

func TestCancelAndTimeout(t *testing.T) {
	for _, cancelMode := range []bool{true, false} {
		for _, write := range []bool{true, false} {
			t.Run(strings.Join([]string{map[bool]string{true: "cancel", false: "timeout"}[cancelMode], map[bool]string{true: "write", false: "read"}[write]}, "/"), func(t *testing.T) {
				entered := make(chan struct{})
				release := make(chan struct{})
				var calls atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(entered); <-release }))
				defer s.Close()
				defer close(release)
				hc := &http.Client{Timeout: 100 * time.Millisecond}
				c := newClient(t, s.URL, &client.Options{HTTPClient: hc})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					if write {
						_, err = c.Put(ctx, "x", "v", "")
					} else {
						_, err = c.Get(ctx, "x", "")
					}
					done <- err
				}()
				<-entered
				if cancelMode {
					cancel()
				}
				err := <-done
				target := context.DeadlineExceeded
				if cancelMode {
					target = context.Canceled
				}
				if !errors.Is(err, target) || errors.Is(err, client.ErrUnknownOutcome) != write || calls.Load() != 1 {
					t.Fatal(err, calls.Load())
				}
			})
		}
	}
}

func TestDisconnectAfterSendNoRetry(t *testing.T) {
	for _, op := range []string{"create", "put", "delete", "cas", "lock", "unlock"} {
		t.Run(op, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				calls.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
			}))
			defer s.Close()
			c := newClient(t, s.URL, nil)
			ctx := context.Background()
			var err error
			switch op {
			case "create":
				_, err = c.Create(ctx, "x", "v", "")
			case "put":
				_, err = c.Put(ctx, "x", "v", "")
			case "delete":
				_, err = c.Delete(ctx, "x", "")
			case "cas":
				_, err = c.CAS(ctx, "x", "a", "v", "")
			case "lock":
				_, err = c.Lock(ctx, "x")
			case "unlock":
				_, err = c.Unlock(ctx, "x", "1")
			}
			var unknown *client.UnknownOutcomeError
			if !errors.As(err, &unknown) || !errors.Is(err, client.ErrUnknownOutcome) || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
}

func TestEscapedDotKeysAndValidation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/kv/%2E" && r.URL.EscapedPath() != "/kv/%2E%2E" {
			t.Error(r.URL.String())
		}
		json.NewEncoder(w).Encode(client.Result{})
	}))
	defer s.Close()
	c := newClient(t, s.URL, nil)
	for _, key := range []string{".", ".."} {
		if _, err := c.Get(context.Background(), key, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"", strings.Repeat("a", 1025), "\xff"} {
		if _, err := c.Put(context.Background(), key, "v", ""); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
