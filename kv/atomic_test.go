package kv

import "testing"

func TestCASValueComparisonAndABA(t *testing.T) {
	s := New()
	commands := []Command{
		{Op: "cas", Key: "x", Expected: "", Value: "missing"},
		{Op: "put", Key: "x", Value: ""},
		{Op: "cas", Key: "x", Expected: "", Value: "a"},
		{Op: "cas", Key: "x", Expected: "a", Value: "b"},
		{Op: "cas", Key: "x", Expected: "a", Value: "loser"},
		{Op: "cas", Key: "x", Expected: "b", Value: "a"},
		// CAS compares values, so returning to a does not invalidate expected=a.
		{Op: "cas", Key: "x", Expected: "a", Value: "a"},
		{Op: "cas", Key: "x", Expected: "a", Value: "c"},
	}
	codes := []int{404, 200, 200, 200, 409, 200, 200, 200}
	for i, c := range commands {
		r := s.Apply(c, uint64(i+1))
		if r.Code != codes[i] || r.Index != uint64(i+1) {
			t.Fatalf("command %d: %+v", i, r)
		}
		if i == 4 && r.Value != "b" {
			t.Fatalf("failed CAS must report current value: %+v", r)
		}
	}
	if got := s.Get("x", ""); got.Value != "c" {
		t.Fatal(got)
	}
}

func TestLockAccessAndKeyLifetime(t *testing.T) {
	for _, exists := range []bool{false, true} {
		for _, op := range []string{"get", "create", "put", "delete", "cas", "unlock", "lock"} {
			for _, token := range []string{"", "foreign", "7"} {
				t.Run(op+"/"+token+map[bool]string{false: "/missing", true: "/present"}[exists], func(t *testing.T) {
					s := New()
					if exists {
						s.Apply(Command{Op: "put", Key: "x", Value: "a"}, 1)
					}
					if r := s.Apply(Command{Op: "lock", Key: "x"}, 7); r.Code != 201 || r.Token != "7" {
						t.Fatal(r)
					}
					before := s.Get("x", "7")
					c := Command{Op: op, Key: "x", Token: token, Expected: "a", Value: "b"}
					var r Result
					if op == "get" {
						r = s.Get("x", token)
					} else {
						r = s.Apply(c, 8)
					}
					want := 423
					if token == "7" {
						want = 200
						switch op {
						case "lock":
							want = 423 // Not reentrant, even for the owner.
						case "create":
							want = 201
							if exists {
								want = 409
							}
						case "get", "delete", "cas":
							if !exists {
								want = 404
							}
						}
					}
					if r.Code != want {
						t.Fatalf("got %+v, want %d", r, want)
					}
					if r.Code >= 400 && (s.locks["x"] != "7" || s.Get("x", "7") != before) {
						t.Fatal("rejected operation changed state")
					}
				})
			}
		}
	}

	s := New()
	steps := []struct {
		c    Command
		code int
	}{
		{Command{Op: "lock", Key: "x"}, 201},
		{Command{Op: "create", Key: "x", Token: "1", Value: "a"}, 201},
		{Command{Op: "delete", Key: "x", Token: "1"}, 200},
		{Command{Op: "create", Key: "x", Value: "intruder"}, 423},
		{Command{Op: "create", Key: "x", Token: "1", Value: "b"}, 201},
		{Command{Op: "unlock", Key: "x", Token: "1"}, 200},
	}
	for i, step := range steps {
		if r := s.Apply(step.c, uint64(i+1)); r.Code != step.code {
			t.Fatalf("step %d: %+v", i, r)
		}
	}
	for i, op := range []string{"get", "create", "put", "delete", "cas", "unlock"} {
		var r Result
		if op == "get" {
			r = s.Get("x", "1")
		} else {
			r = s.Apply(Command{Op: op, Key: "x", Token: "1"}, uint64(10+i))
		}
		if r.Code != 409 {
			t.Fatalf("stale token %s: %+v", op, r)
		}
	}
	if r := s.Apply(Command{Op: "unlock", Key: "x"}, 21); r.Code != 409 {
		t.Fatal(r)
	}
	if r := s.Apply(Command{Op: "lock", Key: "x"}, 42); r.Token != "42" {
		t.Fatal(r)
	}
	if got := s.Get("x", "42"); got.Value != "b" {
		t.Fatal(got)
	}
}
