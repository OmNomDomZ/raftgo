package kv

import (
	"reflect"
	"testing"
)

func TestDeterministicReplayAndFencing(t *testing.T) {
	commands := []Command{
		{Op: "create", Key: "x", Value: "a"},
		{Op: "create", Key: "x", Value: "bad"},
		{Op: "cas", Key: "x", Expected: "wrong", Value: "bad"},
		{Op: "lock", Key: "x"},
		{Op: "put", Key: "x", Value: "bad"},
		{Op: "lock", Key: "x"},
		{Op: "cas", Key: "x", Token: "4", Expected: "a", Value: "b"},
		{Op: "unlock", Key: "x", Token: "wrong"},
		{Op: "unlock", Key: "x", Token: "4"},
		{Op: "lock", Key: "x"},
		{Op: "put", Key: "x", Token: "4", Value: "bad"},
		{Op: "unlock", Key: "x", Token: "4"},
		{Op: "unlock", Key: "x", Token: "10"},
		{Op: "put", Key: "x", Token: "10", Value: "bad"},
	}
	codes := []int{201, 409, 409, 201, 423, 423, 200, 423, 200, 201, 423, 423, 200, 409}
	a, b := New(), New()
	for i, c := range commands {
		x, y := a.Apply(c, uint64(i+1)), b.Apply(c, uint64(i+1))
		if x.Code != codes[i] || x != y {
			t.Fatalf("command %d: %+v %+v", i, x, y)
		}
	}
	if !reflect.DeepEqual(a, b) || a.Get("x", "").Value != "b" {
		t.Fatal("replay diverged or rejected operation changed value")
	}
}

func TestCRUDAndLockReads(t *testing.T) {
	s := New()
	if s.Get("x", "").Code != 404 {
		t.Fatal("missing key")
	}
	s.Apply(Command{Op: "put", Key: "x", Value: ""}, 1)
	if s.Get("x", "").Code != 200 {
		t.Fatal("empty string must exist")
	}
	s.Apply(Command{Op: "lock", Key: "x"}, 2)
	if s.Get("x", "").Code != 423 || s.Get("x", "2").Code != 200 {
		t.Fatal("read lock bypass")
	}
	if s.Apply(Command{Op: "delete", Key: "x"}, 3).Code != 423 {
		t.Fatal("delete bypassed lock")
	}
	if s.Apply(Command{Op: "delete", Key: "x", Token: "2"}, 4).Code != 200 {
		t.Fatal("authorized delete")
	}
	if s.Apply(Command{Op: "create", Key: "x", Value: "bad"}, 5).Code != 423 {
		t.Fatal("delete removed lock")
	}
	s.Apply(Command{Op: "unlock", Key: "x", Token: "2"}, 6)
	if s.Get("x", "").Code != 404 {
		t.Fatal("delete failed")
	}
}
