package storage

import (
	"os"
	"path/filepath"
	"testing"

	"raftgo/raft"
)

func TestRestartPreservesVoteAndLog(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("double open")
	}
	state := raft.PersistentState{CurrentTerm: 3, VotedFor: "A"}
	if err = f.SaveState(state); err != nil {
		t.Fatal(err)
	}
	entries := []raft.LogEntry{{Term: 1, Command: []byte("one")}, {Term: 2, Command: []byte("old")}}
	if err = f.StoreLog(1, entries); err != nil {
		t.Fatal(err)
	}
	entries[0].Command[0] = 'X'
	if err = f.StoreLog(2, []raft.LogEntry{{Term: 3, Command: []byte("new")}}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := f.LoadState()
	if err != nil || got != state {
		t.Fatalf("state: %+v %v", got, err)
	}
	log, err := f.LoadLog()
	if err != nil || len(log) != 2 || string(log[0].Command) != "one" || string(log[1].Command) != "new" {
		t.Fatalf("log: %+v %v", log, err)
	}
}
func TestCorruptStorageFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "raft.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := Open(dir); err == nil {
		f.Close()
		t.Fatal("accepted corrupt state")
	}
}
func TestWriteFailurePoisonsStorage(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Renaming a file over a directory must fail, even when tests run as root.
	path := filepath.Join(dir, "raft.json")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = f.SaveState(raft.PersistentState{CurrentTerm: 1}); err == nil {
		t.Fatal("write succeeded")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = f.SaveState(raft.PersistentState{CurrentTerm: 2}); err == nil {
		t.Fatal("storage recovered after uncertain write")
	}
}
