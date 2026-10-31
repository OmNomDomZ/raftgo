// Package kv implements a deterministic state machine. Its caller serializes access
// and supplies the committed Raft index for each applied result.
package kv

const Prefix = "raftgo:kv:v1:"

type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type Result struct {
	Code  int    `json:"-"`
	Error string `json:"error,omitempty"`
	Value string `json:"value"`
	Index uint64 `json:"index,omitempty"`
}

type Store struct {
	values map[string]string
}

func New() *Store { return &Store{values: make(map[string]string)} }

func (s *Store) Get(key string) Result {
	v, ok := s.values[key]
	if !ok {
		return Result{Code: 404, Error: "key not found"}
	}
	return Result{Code: 200, Value: v}
}

func (s *Store) Apply(c Command, index uint64) Result {
	r := s.apply(c, index)
	r.Index = index
	return r
}
func (s *Store) apply(c Command, index uint64) Result {
	if c.Key == "" {
		return Result{Code: 400, Error: "key is required"}
	}
	_, exists := s.values[c.Key]
	switch c.Op {
	case "create":
		if exists {
			return Result{Code: 409, Error: "key already exists"}
		}
		s.values[c.Key] = c.Value
		return Result{Code: 201, Value: c.Value}
	case "put":
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
