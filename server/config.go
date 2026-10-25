package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"raftgo/raft"
)

type Member struct {
	ID     raft.NodeID `json:"id"`
	RPCURL string      `json:"rpc_url"`
	APIURL string      `json:"api_url"`
}
type Config struct {
	Listen    string   `json:"listen"`
	RPCListen string   `json:"rpc_listen"`
	DataDir   string   `json:"data_dir"`
	Members   []Member `json:"members"`
}

func LoadConfig(path string, id raft.NodeID) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("expected a single config object")
	}
	return c, c.Validate(id)
}
func (c Config) Validate(id raft.NodeID) error {
	if id == "" || c.DataDir == "" {
		return fmt.Errorf("id and data_dir are required")
	}
	for _, addr := range []string{c.Listen, c.RPCListen} {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return err
		}
	}
	if c.Listen == c.RPCListen {
		return fmt.Errorf("API and RPC listeners must differ")
	}
	seen := map[raft.NodeID]bool{}
	endpoints := map[string]bool{}
	for _, m := range c.Members {
		if m.ID == "" || m.ID == "." || m.ID == ".." || strings.ContainsAny(string(m.ID), "/\\") || seen[m.ID] {
			return fmt.Errorf("empty or duplicate member ID %q", m.ID)
		}
		seen[m.ID] = true
		for _, address := range []string{m.RPCURL, m.APIURL} {
			u, err := url.Parse(address)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("invalid base URL %q", address)
			}
			if endpoints[address] {
				return fmt.Errorf("duplicate endpoint %q", address)
			}
			endpoints[address] = true
		}
	}
	if !seen[id] {
		return fmt.Errorf("node %q is not a cluster member", id)
	}
	return nil
}
