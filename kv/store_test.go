package kv

import (
	"reflect"
	"testing"
)

func TestDeterministicCRUDReplay(t *testing.T) {
	commands := []Command{
		{Op: "create", Key: "x", Value: ""},
		{Op: "create", Key: "x", Value: "bad"},
		{Op: "put", Key: "x", Value: "строка\n{\"x\":1}"},
		{Op: "delete", Key: "x"},
		{Op: "delete", Key: "x"},
		{Op: "put", Key: "x", Value: "upsert"},
		{Op: "unknown", Key: "x", Value: "bad"},
		{Op: "put", Value: "bad"},
	}
	codes := []int{201, 409, 200, 200, 404, 200, 400, 400}
	a, b := New(), New()
	if a.Get("x").Code != 404 {
		t.Fatal("missing key")
	}
	for i, c := range commands {
		x, y := a.Apply(c, uint64(i+1)), b.Apply(c, uint64(i+1))
		if x != y || x.Code != codes[i] || x.Index != uint64(i+1) {
			t.Fatalf("command %d: %+v %+v", i, x, y)
		}
		if i == 0 && a.Get("x").Code != 200 {
			t.Fatal("empty value must exist")
		}
		if i == 1 && a.Get("x").Value != "" {
			t.Fatal("duplicate create changed value")
		}
		if i == 2 && a.Get("x").Value != c.Value {
			t.Fatal("string changed")
		}
		if i == 3 && a.Get("x").Code != 404 {
			t.Fatal("delete failed")
		}
	}
	if !reflect.DeepEqual(a, b) || a.Get("x").Value != "upsert" {
		t.Fatal("replay diverged")
	}
}
