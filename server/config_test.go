package server

import (
	"testing"

	"raftgo/raft"
)

func TestConfigValidation(t *testing.T) {
	valid := func() Config {
		return Config{Listen: ":8080", RPCListen: ":9000", DataDir: "/data", Members: []Member{{ID: "n", RPCURL: "http://n:9000", APIURL: "http://localhost:8001"}}}
	}
	if err := valid().Validate("n"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Members = append(c.Members, c.Members[0]) },
		func(c *Config) { c.Members[0].RPCURL = "ftp://n:9000" },
		func(c *Config) { c.Members[0].APIURL = "http://localhost:8001/path" },
		func(c *Config) { c.Members[0].ID = raft.NodeID("../n") },
		func(c *Config) { c.Listen = c.RPCListen },
		func(c *Config) { c.DataDir = "" },
	} {
		c := valid()
		mutate(&c)
		if err := c.Validate("n"); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	if err := valid().Validate("missing"); err == nil {
		t.Fatal("accepted missing self")
	}
}
