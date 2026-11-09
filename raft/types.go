package raft

// NodeID — идентификатор участника кластера.
type NodeID string

// Term — номер раунда Raft.
type Term uint64

// Index — позиция записи лога; пользовательские и no-op записи начинаются с 1.
type Index uint64
