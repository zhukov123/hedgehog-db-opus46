package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all server configuration.
type Config struct {
	// Node identity
	NodeID   string `json:"node_id"`
	BindAddr string `json:"bind_addr"` // e.g. "0.0.0.0:8080"
	DataDir  string `json:"data_dir"`

	// Cluster
	SeedNodes      []string `json:"seed_nodes"`       // addresses of seed nodes to join
	VirtualNodes   int      `json:"virtual_nodes"`     // vnodes per physical node (default 128)
	ReplicationN   int      `json:"replication_n"`     // replication factor (default 3)
	ReadQuorum     int      `json:"read_quorum"`       // R (default 2)
	WriteQuorum    int      `json:"write_quorum"`      // W (default 2)

	// Storage
	BufferPoolSize int `json:"buffer_pool_size"` // pages (default 1024)

	// Membership
	HeartbeatIntervalMS int `json:"heartbeat_interval_ms"` // default 1000
	FailureTimeoutMS    int `json:"failure_timeout_ms"`    // default 5000

	// Anti-entropy
	AntiEntropyIntervalSec int `json:"anti_entropy_interval_sec"` // default 1800 (30 min)
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		NodeID:                 "node-1",
		BindAddr:               "0.0.0.0:8080",
		DataDir:                "./data",
		VirtualNodes:           128,
		ReplicationN:           3,
		ReadQuorum:             2,
		WriteQuorum:            2,
		BufferPoolSize:         1024,
		HeartbeatIntervalMS:    1000,
		FailureTimeoutMS:       5000,
		AntiEntropyIntervalSec: 1800,
	}
}

// LoadConfig loads a configuration from a JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// Validate checks the config for consistency.
func (c *Config) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	if c.BindAddr == "" {
		return fmt.Errorf("bind_addr is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if c.ReplicationN < 1 {
		return fmt.Errorf("replication_n must be >= 1")
	}
	if c.ReadQuorum < 1 || c.ReadQuorum > c.ReplicationN {
		return fmt.Errorf("read_quorum must be between 1 and replication_n")
	}
	if c.WriteQuorum < 1 || c.WriteQuorum > c.ReplicationN {
		return fmt.Errorf("write_quorum must be between 1 and replication_n")
	}
	return nil
}
