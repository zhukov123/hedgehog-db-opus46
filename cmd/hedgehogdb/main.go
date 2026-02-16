package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	hedgehog "github.com/hedgehog-db/hedgehog"
	"github.com/hedgehog-db/hedgehog/internal/api"
	"github.com/hedgehog-db/hedgehog/internal/cluster"
	"github.com/hedgehog-db/hedgehog/internal/config"
	"github.com/hedgehog-db/hedgehog/internal/metrics"
	"github.com/hedgehog-db/hedgehog/internal/table"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
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

	// --- OpenTelemetry tracer provider ---
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4318"
	}
	traceExporter, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint(otlpEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		log.Printf("WARNING: failed to create OTLP exporter (traces disabled): %v", err)
	} else {
		res, _ := resource.Merge(
			resource.Default(),
			resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceName("hedgehogdb"),
				semconv.ServiceInstanceID(cfg.NodeID),
			),
		)
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tp.Shutdown(shutdownCtx); err != nil {
				log.Printf("Tracer provider shutdown error: %v", err)
			}
		}()
		log.Printf("OpenTelemetry tracing enabled (OTLP endpoint: %s)", otlpEndpoint)
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

	// When a node recovers from Dead/Suspect, immediately replay pending hints.
	membership.SetRecoveryCallback(func(nodeID string) {
		replicator.ReplayHints(nodeID)
	})

	antiEntropy := cluster.NewAntiEntropy(
		membership, ring, tableManager,
		time.Duration(cfg.AntiEntropyIntervalSec)*time.Second,
	)

	// Create API server (coordinator enables replication for PUT/GET/DELETE)
	server := api.NewServer(cfg.BindAddr, tableManager, coordinator)

	// Register cluster routes
	coordinator.RegisterInternalRoutes(server.Router())
	antiEntropy.RegisterRoutes(server.Router())

	// Prometheus metrics endpoint
	server.Router().GET("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})

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

	// Cluster-wide table scan: merge items from all nodes so the UI shows full count
	clusterScanClient := &http.Client{Timeout: 10 * time.Second}
	server.Router().GET("/api/v1/cluster/tables/{name}/items", func(w http.ResponseWriter, r *http.Request) {
		tableName := api.Param(r, "name")
		if tableName == "" {
			http.Error(w, `{"error":"table name required"}`, http.StatusBadRequest)
			return
		}
		nodes := membership.GetAllNodes()
		merged := make(map[string]map[string]interface{}) // key -> { "key": k, "item": doc }
		for _, node := range nodes {
			base := strings.TrimSpace(node.Addr)
			if base == "" {
				continue
			}
			if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
				base = "http://" + base
			}
			base = strings.TrimSuffix(base, "/")
			url := base + "/api/v1/tables/" + tableName + "/items"
			resp, err := clusterScanClient.Get(url)
			if err != nil {
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}
			var data struct {
				Items []struct {
					Key  string                 `json:"key"`
					Item map[string]interface{} `json:"item"`
				} `json:"items"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
			for _, e := range data.Items {
				merged[e.Key] = map[string]interface{}{"key": e.Key, "item": e.Item}
			}
		}
		items := make([]map[string]interface{}, 0, len(merged))
		for _, v := range merged {
			items = append(items, v)
		}
		sort.Slice(items, func(i, j int) bool {
			ki, _ := items[i]["key"].(string)
			kj, _ := items[j]["key"].(string)
			return ki < kj
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": items,
			"count": len(items),
		})
	})

	// Per-node table counts for verification (e.g. curl http://localhost:8081/api/v1/cluster/table-counts)
	server.Router().GET("/api/v1/cluster/table-counts", func(w http.ResponseWriter, r *http.Request) {
		nodes := membership.GetAllNodes()
		tables := []string{"users", "products", "orders"}
		result := make(map[string]map[string]int)
		for _, name := range tables {
			result[name] = make(map[string]int)
		}
		for _, node := range nodes {
			base := strings.TrimSpace(node.Addr)
			if base == "" {
				continue
			}
			if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
				base = "http://" + base
			}
			base = strings.TrimSuffix(base, "/")
			for _, name := range tables {
				resp, err := clusterScanClient.Get(base + "/api/v1/tables/" + name + "/count")
				if err != nil {
					result[name][node.ID] = -1
					continue
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					result[name][node.ID] = -1
					continue
				}
				var data struct {
					Count int `json:"count"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
					resp.Body.Close()
					result[name][node.ID] = -1
					continue
				}
				resp.Body.Close()
				result[name][node.ID] = data.Count
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
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

	server.Router().DELETE("/api/v1/cluster/nodes/{node_id}", func(w http.ResponseWriter, r *http.Request) {
		nodeID := api.Param(r, "node_id")
		if nodeID == "" {
			http.Error(w, `{"error":"node_id required"}`, http.StatusBadRequest)
			return
		}
		if err := membership.RemoveNode(nodeID); err != nil {
			if err == cluster.ErrCannotRemoveSelf {
				http.Error(w, `{"error":"cannot remove self from cluster"}`, http.StatusBadRequest)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "node removed",
		})
	})

	// Debug endpoints
	server.Router().GET("/api/v1/debug/replica-nodes", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"key query param required"}`, http.StatusBadRequest)
			return
		}
		nodes := ring.GetNodes(key, cfg.ReplicationN)
		primary := ""
		replicas := make([]string, 0)
		if len(nodes) > 0 {
			primary = nodes[0]
			replicas = nodes[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"primary":  primary,
			"replicas": replicas,
		})
	})

	server.Router().GET("/api/v1/debug/replication-backlog", func(w http.ResponseWriter, r *http.Request) {
		handoff := replicator.Handoff()
		pendingPerNode := handoff.PendingCounts()
		hints := handoff.PendingHints()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pending_per_node": pendingPerNode,
			"hints":            hints,
		})
	})

	server.Router().POST("/api/v1/debug/replay-hints/{node_id}", func(w http.ResponseWriter, r *http.Request) {
		nodeID := api.Param(r, "node_id")
		if nodeID == "" {
			http.Error(w, `{"error":"node_id required"}`, http.StatusBadRequest)
			return
		}
		replicator.ReplayHints(nodeID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "hint replay triggered",
		})
	})

	// Start background services
	membership.Start()
	antiEntropy.Start()
	replicator.StartReplayLoop()

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
	replicator.StopReplayLoop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	log.Println("HedgehogDB shut down gracefully")
}
