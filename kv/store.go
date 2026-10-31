// Package kv implements a deterministic state machine. Its caller serializes access
// and supplies the committed Raft index as a monotonically increasing fence.
package kv

import "strconv"

const Prefix = "raftgo:kv:v1:"

type Command struct {
	Op       string `json:"op"`
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Expected string `json:"expected,omitempty"`
	Token    string `json:"token,omitempty"`
}

type Result struct {
	Code  int    `json:"-"`
	Error string `json:"error,omitempty"`
	Value string `json:"value"`
	Token string `json:"token,omitempty"`
	Index uint64 `json:"index,omitempty"`
}

type Store struct {
	values map[string]string
	locks  map[string]string
}

func New() *Store { return &Store{values: make(map[string]string), locks: make(map[string]string)} }

func (s *Store) access(key, token string) Result {
	current := s.locks[key]
	if current != "" && current != token {
		return Result{Code: 423, Error: "key is locked"}
	}
	if token != "" && current != token {
		return Result{Code: 409, Error: "stale lock token"}
	}
	return Result{Code: 200}
}

func (s *Store) Get(key, token string) Result {
	if r := s.access(key, token); r.Code != 200 {
		return r
	}
	v, ok := s.values[key]
	if !ok {
		return Result{Code: 404, Error: "key not found"}
	}
	return Result{Code: 200, Value: v}
}

// Apply executes one committed command, including its access check and CAS
// comparison, atomically with respect to the caller's serialization. index must
// be the positive Raft index, delivered once in increasing order (gaps for raw
// entries and no-ops are allowed). Replay uses a fresh Store and the same indices;
// this method does not deduplicate client retries or repeated delivery.
func (s *Store) Apply(c Command, index uint64) Result {
	r := s.apply(c, index)
	r.Index = index
	return r
}
func (s *Store) apply(c Command, index uint64) Result {
	if c.Key == "" {
		return Result{Code: 400, Error: "key is required"}
	}
	if c.Op == "lock" {
		if s.locks[c.Key] != "" {
			return Result{Code: 423, Error: "key is locked"}
		}
		token := strconv.FormatUint(index, 10)
		s.locks[c.Key] = token
		return Result{Code: 201, Token: token}
	}
	if r := s.access(c.Key, c.Token); r.Code != 200 {
		return r
	}
	v, exists := s.values[c.Key]
	switch c.Op {
	case "unlock":
		if c.Token == "" {
			return Result{Code: 409, Error: "lock token is required"}
		}
		delete(s.locks, c.Key)
	case "create":
		if exists {
			return Result{Code: 409, Error: "key already exists"}
		}
		s.values[c.Key] = c.Value
		return Result{Code: 201, Value: c.Value}
	case "put":
		s.values[c.Key] = c.Value
	case "cas":
		if !exists {
			return Result{Code: 404, Error: "key not found"}
		}
		if v != c.Expected {
			return Result{Code: 409, Error: "compare failed", Value: v}
		}
		s.values[c.Key] = c.Value
	case "delete":
		if !exists {
			return Result{Code: 404, Error: "key not found"}
		}
		delete(s.values, c.Key)
	default:
		return Result{Code: 400, Error: "unknown operation"}
	}
	return Result{Code: 200, Value: c.Value}
}
