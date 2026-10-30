package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"raftgo/raft"
	"raftgo/server"
	"raftgo/storage"
	"raftgo/transport"
)

func main() {
	if err := run(); err != nil {
		slog.Error("node failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "config/cluster.json", "cluster config file")
	id := flag.String("id", "", "node ID from config")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("node", *id))
	cfg, err := server.LoadConfig(*path, raft.NodeID(*id))
	if err != nil {
		return err
	}
	memory := &storage.Memory{}
	peers := map[raft.NodeID]string{}
	var ids []raft.NodeID
	for _, m := range cfg.Members {
		if m.ID != raft.NodeID(*id) {
			peers[m.ID] = m.RPCURL
			ids = append(ids, m.ID)
		}
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	node, err := raft.New(raft.Config{ID: raft.NodeID(*id), Peers: ids, Storage: memory, Transport: &transport.HTTP{Peers: peers, Client: client}, Logger: slog.Default(), ElectionTimeoutMin: 800 * time.Millisecond, ElectionTimeoutMax: 1600 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond, AppendEntriesTimeout: 400 * time.Millisecond, RequestVoteTimeout: 400 * time.Millisecond})
	if err != nil {
		return err
	}
	api := server.NewAPI(node, cfg.Members)
	public, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer public.Close()
	internal, err := net.Listen("tcp", cfg.RPCListen)
	if err != nil {
		return err
	}
	defer internal.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	nodeDone := make(chan struct{})
	go func() { defer close(nodeDone); node.Run(ctx) }()
	apiServer := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	rpcServer := &http.Server{Handler: server.RPCHandler(node), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 2)
	go func() { errs <- apiServer.Serve(public) }()
	go func() { errs <- rpcServer.Serve(internal) }()
	slog.Info("listening", "id", *id, "api", cfg.Listen, "rpc", cfg.RPCListen)
	select {
	case <-ctx.Done():
	case err = <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancel()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancelShutdown()
	for _, s := range []*http.Server{apiServer, rpcServer} {
		if e := s.Shutdown(shutdown); e != nil {
			_ = s.Close()
			if err == nil {
				err = fmt.Errorf("shutdown: %w", e)
			}
		}
	}
	<-nodeDone
	return err
}
