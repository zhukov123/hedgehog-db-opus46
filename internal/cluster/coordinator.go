package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hedgehog-db/hedgehog/internal/metrics"
	"github.com/hedgehog-db/hedgehog/internal/storage"
	"github.com/hedgehog-db/hedgehog/internal/table"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var coordTracer = otel.Tracer("hedgehogdb.coordinator")

// drainClose fully reads and closes a response body so the TCP connection
// can be returned to the pool for reuse.
func drainClose(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// Coordinator routes requests to the appropriate nodes.
type Coordinator struct {
	membership   *Membership
	ring         *Ring
	tableManager *table.TableManager
	replicator   *Replicator
	readQuorum   int
	writeQuorum  int
	replN        int
	client       *http.Client
}

// NewCoordinator creates a request coordinator.
func NewCoordinator(membership *Membership, ring *Ring, tm *table.TableManager, replN, readQ, writeQ int) *Coordinator {
	return &Coordinator{
		membership:   membership,
		ring:         ring,
		tableManager: tm,
		readQuorum:   readQ,
		writeQuorum:  writeQ,
		replN:        replN,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// SetReplicator sets the replicator (called after construction to break circular dependency).
func (c *Coordinator) SetReplicator(r *Replicator) {
	c.replicator = r
}

// RouteGet routes a GET request for a key.
func (c *Coordinator) RouteGet(ctx context.Context, tableName, key, consistency string) (map[string]interface{}, error) {
	ctx, span := coordTracer.Start(ctx, "coordinator.route_get",
		trace.WithAttributes(
			attribute.String("table", tableName),
			attribute.String("key", key),
			attribute.String("consistency", consistency),
		),
	)
	defer span.End()

	nodes := c.ring.GetNodes(key, c.replN)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	selfID := c.membership.SelfID()

	if consistency == "strong" {
		return c.quorumRead(ctx, tableName, key, nodes)
	}

	// Eventual: try to read locally if we're a replica
	for _, nodeID := range nodes {
		if nodeID == selfID {
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				continue
			}
			doc, err := t.GetItem(key)
			if err != nil {
				continue
			}
			return doc, nil
		}
	}

	// Forward to the coordinator node
	return c.forwardGet(ctx, nodes[0], tableName, key)
}

// RoutePut routes a PUT request for a key.
func (c *Coordinator) RoutePut(ctx context.Context, tableName, key string, doc map[string]interface{}, consistency string) error {
	ctx, span := coordTracer.Start(ctx, "coordinator.route_put",
		trace.WithAttributes(
			attribute.String("table", tableName),
			attribute.String("key", key),
			attribute.String("consistency", consistency),
		),
	)
	defer span.End()

	nodes := c.ring.GetNodes(key, c.replN)
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes available")
	}

	selfID := c.membership.SelfID()
	isReplica := false
	for _, n := range nodes {
		if n == selfID {
			isReplica = true
			break
		}
	}

	// Strong consistency: quorumWrite handles all writes (local + remote).
	if consistency == "strong" {
		return c.quorumWrite(ctx, tableName, key, doc, nodes)
	}

	// Eventual: write locally if we're a replica, then async replicate.
	if isReplica {
		t, err := c.tableManager.GetTable(tableName)
		if err != nil {
			return err
		}
		if err := t.PutItem(key, doc); err != nil {
			return err
		}
	}

	if c.replicator != nil {
		go c.replicator.ReplicateWrite(tableName, key, doc, nodes)
	}

	// If we're not a replica, forward to coordinator
	if !isReplica {
		return c.forwardPut(ctx, nodes[0], tableName, key, doc)
	}

	return nil
}

// RouteDelete routes a DELETE request for a key.
func (c *Coordinator) RouteDelete(ctx context.Context, tableName, key, consistency string) error {
	ctx, span := coordTracer.Start(ctx, "coordinator.route_delete",
		trace.WithAttributes(
			attribute.String("table", tableName),
			attribute.String("key", key),
			attribute.String("consistency", consistency),
		),
	)
	defer span.End()

	nodes := c.ring.GetNodes(key, c.replN)
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes available")
	}

	selfID := c.membership.SelfID()
	isReplica := false
	for _, n := range nodes {
		if n == selfID {
			isReplica = true
			break
		}
	}

	// Strong consistency: quorumDelete handles all deletes (local + remote).
	if consistency == "strong" {
		return c.quorumDelete(ctx, tableName, key, nodes)
	}

	// Eventual: delete locally if we're a replica, then async replicate.
	if isReplica {
		t, err := c.tableManager.GetTable(tableName)
		if err != nil {
			return err
		}
		if err := t.DeleteItem(key); err != nil {
			return err
		}
	}

	if c.replicator != nil {
		go c.replicator.ReplicateDelete(tableName, key, nodes)
	}

	if !isReplica {
		return c.forwardDelete(ctx, nodes[0], tableName, key)
	}

	return nil
}

// quorumRead reads from R replicas and returns the latest value.
func (c *Coordinator) quorumRead(ctx context.Context, tableName, key string, nodes []string) (map[string]interface{}, error) {
	type readResult struct {
		doc map[string]interface{}
		err error
	}

	results := make(chan readResult, len(nodes))
	selfID := c.membership.SelfID()

	for _, nodeID := range nodes {
		if nodeID == selfID {
			go func() {
				t, err := c.tableManager.GetTable(tableName)
				if err != nil {
					results <- readResult{err: err}
					return
				}
				doc, err := t.GetItem(key)
				results <- readResult{doc: doc, err: err}
			}()
		} else {
			go func(nid string) {
				doc, err := c.forwardGet(ctx, nid, tableName, key)
				results <- readResult{doc: doc, err: err}
			}(nodeID)
		}
	}

	var lastDoc map[string]interface{}
	successCount := 0
	for i := 0; i < len(nodes); i++ {
		r := <-results
		if r.err == nil {
			successCount++
			lastDoc = r.doc
		}
		if successCount >= c.readQuorum {
			return lastDoc, nil
		}
	}

	if lastDoc != nil {
		return lastDoc, nil
	}
	return nil, fmt.Errorf("quorum read failed: got %d/%d responses", successCount, c.readQuorum)
}

// quorumWrite writes to W replicas synchronously.
func (c *Coordinator) quorumWrite(ctx context.Context, tableName, key string, doc map[string]interface{}, nodes []string) error {
	results := make(chan error, len(nodes))
	selfID := c.membership.SelfID()

	for _, nodeID := range nodes {
		if nodeID == selfID {
			go func() {
				t, err := c.tableManager.GetTable(tableName)
				if err != nil {
					results <- err
					return
				}
				results <- t.PutItem(key, doc)
			}()
		} else {
			go func(nid string) {
				results <- c.forwardPut(ctx, nid, tableName, key, doc)
			}(nodeID)
		}
	}

	successCount := 0
	for i := 0; i < len(nodes); i++ {
		if err := <-results; err == nil {
			successCount++
		}
		if successCount >= c.writeQuorum {
			return nil
		}
	}

	return fmt.Errorf("quorum write failed: got %d/%d acks", successCount, c.writeQuorum)
}

// quorumDelete deletes from W replicas synchronously.
func (c *Coordinator) quorumDelete(ctx context.Context, tableName, key string, nodes []string) error {
	results := make(chan error, len(nodes))
	selfID := c.membership.SelfID()

	for _, nodeID := range nodes {
		if nodeID == selfID {
			go func() {
				t, err := c.tableManager.GetTable(tableName)
				if err != nil {
					results <- err
					return
				}
				results <- t.DeleteItem(key)
			}()
		} else {
			go func(nid string) {
				results <- c.forwardDelete(ctx, nid, tableName, key)
			}(nodeID)
		}
	}

	successCount := 0
	for i := 0; i < len(nodes); i++ {
		if err := <-results; err == nil {
			successCount++
		}
		if successCount >= c.writeQuorum {
			return nil
		}
	}

	return fmt.Errorf("quorum delete failed: got %d/%d acks", successCount, c.writeQuorum)
}

// Forward helpers — inject trace context into outgoing requests.

func (c *Coordinator) injectTrace(ctx context.Context, req *http.Request) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
}

func (c *Coordinator) forwardGet(ctx context.Context, nodeID, tableName, key string) (map[string]interface{}, error) {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return nil, fmt.Errorf("unknown node %s", nodeID)
	}

	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=get", addr, tableName, key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.injectTrace(ctx, req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forward get to %s: %w", nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, storage.ErrKeyNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("forward get to %s: status %d: %s", nodeID, resp.StatusCode, body)
	}

	var result struct {
		Item map[string]interface{} `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Item, nil
}

func (c *Coordinator) forwardPut(ctx context.Context, nodeID, tableName, key string, doc map[string]interface{}) error {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return fmt.Errorf("unknown node %s", nodeID)
	}

	body, _ := json.Marshal(doc)
	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", addr, tableName, key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.injectTrace(ctx, req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("forward put to %s: %w", nodeID, err)
	}
	drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forward put to %s: status %d", nodeID, resp.StatusCode)
	}
	return nil
}

func (c *Coordinator) forwardDelete(ctx context.Context, nodeID, tableName, key string) error {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return fmt.Errorf("unknown node %s", nodeID)
	}

	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, tableName, key)
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	c.injectTrace(ctx, req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("forward delete to %s: %w", nodeID, err)
	}
	drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forward delete to %s: status %d", nodeID, resp.StatusCode)
	}
	return nil
}

// RegisterInternalRoutes registers the internal cluster communication routes.
func (c *Coordinator) RegisterInternalRoutes(router interface {
	POST(pattern string, handler http.HandlerFunc)
	GET(pattern string, handler http.HandlerFunc)
	DELETE(pattern string, handler http.HandlerFunc)
}) {
	// Heartbeat endpoint
	router.POST("/internal/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var hb struct {
			NodeID string `json:"node_id"`
			Addr   string `json:"addr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		c.membership.HandleHeartbeat(hb.NodeID, hb.Addr)
		w.WriteHeader(http.StatusOK)
	})

	// Replication endpoint — extract trace context, record metrics, start span.
	replicateHandler := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tableName := r.URL.Query().Get("table")
		key := r.URL.Query().Get("key")
		op := r.URL.Query().Get("op")

		if tableName == "" || key == "" {
			http.Error(w, "table and key required", http.StatusBadRequest)
			return
		}

		// Extract incoming trace context and start a child span
		prop := otel.GetTextMapPropagator()
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := coordTracer.Start(ctx, "replicate",
			trace.WithAttributes(
				attribute.String("op", op),
				attribute.String("table", tableName),
				attribute.String("key", key),
			),
		)
		defer span.End()
		_ = ctx // ctx available for any downstream use

		result := "success"
		defer func() {
			dur := time.Since(start).Seconds()
			metrics.ReplicationReceivedTotal.WithLabelValues(op, result).Inc()
			metrics.ReplicationReceivedDuration.WithLabelValues(op, result).Observe(dur)
		}()

		switch op {
		case "get":
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				result = "error"
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			doc, err := t.GetItem(key)
			if err != nil {
				result = "error"
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"item": doc})

		case "put":
			body, _ := io.ReadAll(r.Body)
			var doc map[string]interface{}
			if err := json.Unmarshal(body, &doc); err != nil {
				result = "error"
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				result = "error"
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if err := t.PutItem(key, doc); err != nil {
				result = "error"
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)

		case "delete":
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				result = "error"
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if err := t.DeleteItem(key); err != nil {
				result = "error"
				log.Printf("replicate delete %s/%s: %v", tableName, key, err)
			}
			w.WriteHeader(http.StatusOK)

		default:
			result = "error"
			http.Error(w, "unknown op", http.StatusBadRequest)
		}
	}

	router.POST("/internal/v1/replicate", replicateHandler)
	router.GET("/internal/v1/replicate", replicateHandler)
	router.DELETE("/internal/v1/replicate", replicateHandler)
}
