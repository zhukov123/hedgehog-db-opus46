package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	hedgehog "github.com/hedgehog-db/hedgehog"
	"github.com/hedgehog-db/hedgehog/internal/api"
	"github.com/hedgehog-db/hedgehog/internal/cluster"
	"github.com/hedgehog-db/hedgehog/internal/config"
	"github.com/hedgehog-db/hedgehog/internal/table"
)


func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var (
		configPath string
		nodeID     string
		bindAddr   string
		dataDir    string
		seedNodes  string
	)

	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.StringVar(&nodeID, "node-id", "node-1", "unique node identifier")
	flag.StringVar(&bindAddr, "bind", "0.0.0.0:8080", "bind address")
	flag.StringVar(&dataDir, "data-dir", "./data", "data directory")
	flag.StringVar(&seedNodes, "seed-nodes", "", "comma-separated seed node addresses")
	flag.Parse()

	// Load config
	var cfg *config.Config
	if configPath != "" {
		var err error
		cfg, err = config.LoadConfig(configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		cfg = config.DefaultConfig()
		cfg.NodeID = nodeID
		cfg.BindAddr = bindAddr
		cfg.DataDir = dataDir
		if seedNodes != "" {
			cfg.SeedNodes = strings.Split(seedNodes, ",")
		}
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid config: %v", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize table layer
	catalog, err := table.OpenCatalog(cfg.DataDir)
	if err != nil {
		log.Fatalf("Failed to open catalog: %v", err)
	}

	tableOpts := table.TableOptions{
		DataDir:        cfg.DataDir,
		BufferPoolSize: cfg.BufferPoolSize,
	}
	tableManager := table.NewTableManager(catalog, tableOpts)

	// Initialize cluster
	ring := cluster.NewRing(cfg.VirtualNodes)
	membership := cluster.NewMembership(
		cfg.NodeID,
		cfg.BindAddr,
		ring,
		time.Duration(cfg.HeartbeatIntervalMS)*time.Millisecond,
		time.Duration(cfg.FailureTimeoutMS)*time.Millisecond,
	)

	// Add seed nodes
	for _, seed := range cfg.SeedNodes {
		seed = strings.TrimSpace(seed)
		if seed != "" && seed != cfg.BindAddr {
			membership.AddNode("seed-"+seed, seed)
		}
	}

	coordinator := cluster.NewCoordinator(
		membership, ring, tableManager,
		cfg.ReplicationN, cfg.ReadQuorum, cfg.WriteQuorum,
	)
	replicator := cluster.NewReplicator(membership)
	coordinator.SetReplicator(replicator)

	antiEntropy := cluster.NewAntiEntropy(
		membership, ring, tableManager,
		time.Duration(cfg.AntiEntropyIntervalSec)*time.Second,
	)

	// Create API server
	server := api.NewServer(cfg.BindAddr, tableManager)

	// Register cluster routes
	coordinator.RegisterInternalRoutes(server.Router())
	antiEntropy.RegisterRoutes(server.Router())

	// Serve embedded web UI
	webFS := &hedgehog.WebDist
	api.RegisterWebUI(server.Router(), webFS)

	// Cluster status endpoints
	server.Router().GET("/api/v1/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		nodes := membership.GetAllNodes()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"node_id":       cfg.NodeID,
			"bind_addr":     cfg.BindAddr,
			"replication_n": cfg.ReplicationN,
			"read_quorum":   cfg.ReadQuorum,
			"write_quorum":  cfg.WriteQuorum,
			"total_nodes":   len(nodes),
			"ring_size":     ring.Size(),
			"nodes":         nodes,
		})
	})

	server.Router().GET("/api/v1/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		nodes := membership.GetAllNodes()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": nodes,
		})
	})

	server.Router().POST("/api/v1/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			NodeID string `json:"node_id"`
			Addr   string `json:"addr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		membership.AddNode(req.NodeID, req.Addr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "node added",
		})
	})

	// Start background services
	membership.Start()
	antiEntropy.Start()

	log.Printf("HedgehogDB node %s starting", cfg.NodeID)
	log.Printf("  Bind: %s", cfg.BindAddr)
	log.Printf("  Data: %s", cfg.DataDir)
	log.Printf("  Replication: N=%d R=%d W=%d", cfg.ReplicationN, cfg.ReadQuorum, cfg.WriteQuorum)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigCh
	log.Println("Received shutdown signal")

	membership.Stop()
	antiEntropy.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	log.Println("HedgehogDB shut down gracefully")
}
