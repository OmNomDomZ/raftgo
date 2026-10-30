package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"raftgo/raft"
)

func TestConfigValidation(t *testing.T) {
	valid := func() Config {
		return Config{Listen: ":8080", RPCListen: ":9000", Members: []Member{{ID: "n", RPCURL: "http://n:9000", APIURL: "http://localhost:8001", InternalAPIURL: "http://n:8080"}}}
	}
	if err := valid().Validate("n"); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Members = append(c.Members, c.Members[0]) },
		func(c *Config) { c.Members[0].RPCURL = "ftp://n:9000" },
		func(c *Config) { c.Members[0].APIURL = "http://localhost:8001/path" },
		func(c *Config) { c.Members[0].InternalAPIURL = "http://user:secret@n:8080" },
		func(c *Config) { c.Members[0].InternalAPIURL = "http://n:8080/path" },
		func(c *Config) { c.Members[0].InternalAPIURL = "http://n:8080?url=x" },
		func(c *Config) { c.Members[0].InternalAPIURL = "ftp://n:8080" },
		func(c *Config) { c.Members[0].InternalAPIURL = c.Members[0].RPCURL },
		func(c *Config) { c.Members[0].ID = raft.NodeID("../n") },
		func(c *Config) { c.Listen = c.RPCListen },
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

// Old disk configs must fail loudly rather than suggest that data is durable.
func TestLoadMemoryConfig(t *testing.T) {
	config := `{"listen":":8080","rpc_listen":":9000","members":[{"id":"n","rpc_url":"http://n:9000","api_url":"http://localhost:8001"}]}`
	path := filepath.Join(t.TempDir(), "cluster.json")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, "n"); err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal([]byte(config), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["data_dir"] = "/data"
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, "n"); err == nil {
		t.Fatal("accepted obsolete disk configuration")
	}
}
