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
	// InternalAPIURL is optional: server-side forwarding requests may need a Docker DNS
	// address while APIURL remains reachable by external clients following 307.
	InternalAPIURL string `json:"internal_api_url,omitempty"`
}

func (m Member) apiEndpoint() string {
	if m.InternalAPIURL != "" {
		return m.InternalAPIURL
	}
	return m.APIURL
}

type Config struct {
	Listen    string   `json:"listen"`
	RPCListen string   `json:"rpc_listen"`
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
	if id == "" {
		return fmt.Errorf("id is required")
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
		addresses := []string{m.RPCURL, m.APIURL}
		if m.InternalAPIURL != "" && m.InternalAPIURL != m.APIURL {
			addresses = append(addresses, m.InternalAPIURL)
		}
		for _, address := range addresses {
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
