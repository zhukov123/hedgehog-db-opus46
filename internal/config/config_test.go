package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.NodeID != "node-1" {
		t.Errorf("DefaultConfig NodeID: got %q", cfg.NodeID)
	}
	if cfg.BindAddr != "0.0.0.0:8080" {
		t.Errorf("DefaultConfig BindAddr: got %q", cfg.BindAddr)
	}
	if cfg.ReplicationN != 3 || cfg.ReadQuorum != 2 || cfg.WriteQuorum != 2 {
		t.Errorf("DefaultConfig quorum: N=%d R=%d W=%d", cfg.ReplicationN, cfg.ReadQuorum, cfg.WriteQuorum)
	}
}

func TestConfig_Validate_Valid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate default: %v", err)
	}
	cfg.ReplicationN = 5
	cfg.ReadQuorum = 3
	cfg.WriteQuorum = 3
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate N=5 R=3 W=3: %v", err)
	}
}

func TestConfig_Validate_Invalid(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Config)
		want string
	}{
		{"empty node_id", func(c *Config) { c.NodeID = "" }, "node_id"},
		{"empty bind_addr", func(c *Config) { c.BindAddr = "" }, "bind_addr"},
		{"empty data_dir", func(c *Config) { c.DataDir = "" }, "data_dir"},
		{"replication_n < 1", func(c *Config) { c.ReplicationN = 0 }, "replication_n"},
		{"read_quorum > N", func(c *Config) { c.ReplicationN = 3; c.ReadQuorum = 4 }, "read_quorum"},
		{"write_quorum > N", func(c *Config) { c.ReplicationN = 3; c.WriteQuorum = 4 }, "write_quorum"},
		{"read_quorum < 1", func(c *Config) { c.ReadQuorum = 0 }, "read_quorum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mod(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestConfig_LoadConfig(t *testing.T) {
	dir, err := os.MkdirTemp("", "hedgehog-config-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	content := `{"node_id":"n1","bind_addr":":9090","data_dir":"./data","replication_n":2,"read_quorum":1,"write_quorum":1}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeID != "n1" || cfg.BindAddr != ":9090" {
		t.Errorf("LoadConfig: got node_id=%q bind_addr=%q", cfg.NodeID, cfg.BindAddr)
	}
	if cfg.ReplicationN != 2 || cfg.ReadQuorum != 1 || cfg.WriteQuorum != 1 {
		t.Errorf("LoadConfig quorum: N=%d R=%d W=%d", cfg.ReplicationN, cfg.ReadQuorum, cfg.WriteQuorum)
	}
}

