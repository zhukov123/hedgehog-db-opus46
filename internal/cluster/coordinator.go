package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hedgehog-db/hedgehog/internal/storage"
	"github.com/hedgehog-db/hedgehog/internal/table"
)

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
		},
	}
}

// SetReplicator sets the replicator (called after construction to break circular dependency).
func (c *Coordinator) SetReplicator(r *Replicator) {
	c.replicator = r
}

// RouteGet routes a GET request for a key.
func (c *Coordinator) RouteGet(tableName, key, consistency string) (map[string]interface{}, error) {
	nodes := c.ring.GetNodes(key, c.replN)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	selfID := c.membership.SelfID()

	if consistency == "strong" {
		return c.quorumRead(tableName, key, nodes)
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
	return c.forwardGet(nodes[0], tableName, key)
}

// RoutePut routes a PUT request for a key.
func (c *Coordinator) RoutePut(tableName, key string, doc map[string]interface{}, consistency string) error {
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

	// Write locally if we're a replica
	if isReplica {
		t, err := c.tableManager.GetTable(tableName)
		if err != nil {
			return err
		}
		if err := t.PutItem(key, doc); err != nil {
			return err
		}
	}

	if consistency == "strong" {
		return c.quorumWrite(tableName, key, doc, nodes)
	}

	// Eventual: async replicate to other nodes
	if c.replicator != nil {
		go c.replicator.ReplicateWrite(tableName, key, doc, nodes)
	}

	// If we're not a replica, forward to coordinator
	if !isReplica {
		return c.forwardPut(nodes[0], tableName, key, doc)
	}

	return nil
}

// RouteDelete routes a DELETE request for a key.
func (c *Coordinator) RouteDelete(tableName, key, consistency string) error {
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

	if isReplica {
		t, err := c.tableManager.GetTable(tableName)
		if err != nil {
			return err
		}
		if err := t.DeleteItem(key); err != nil {
			return err
		}
	}

	if consistency == "strong" {
		return c.quorumDelete(tableName, key, nodes)
	}

	// Eventual: async replicate delete
	if c.replicator != nil {
		go c.replicator.ReplicateDelete(tableName, key, nodes)
	}

	if !isReplica {
		return c.forwardDelete(nodes[0], tableName, key)
	}

	return nil
}

// quorumRead reads from R replicas and returns the latest value.
func (c *Coordinator) quorumRead(tableName, key string, nodes []string) (map[string]interface{}, error) {
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
				doc, err := c.forwardGet(nid, tableName, key)
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
func (c *Coordinator) quorumWrite(tableName, key string, doc map[string]interface{}, nodes []string) error {
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
				results <- c.forwardPut(nid, tableName, key, doc)
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
func (c *Coordinator) quorumDelete(tableName, key string, nodes []string) error {
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
				results <- c.forwardDelete(nid, tableName, key)
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

// Forward helpers
func (c *Coordinator) forwardGet(nodeID, tableName, key string) (map[string]interface{}, error) {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return nil, fmt.Errorf("unknown node %s", nodeID)
	}

	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=get", addr, tableName, key)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("forward get to %s: %w", nodeID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
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

func (c *Coordinator) forwardPut(nodeID, tableName, key string, doc map[string]interface{}) error {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return fmt.Errorf("unknown node %s", nodeID)
	}

	body, _ := json.Marshal(doc)
	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", addr, tableName, key)
	resp, err := c.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("forward put to %s: %w", nodeID, err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forward put to %s: status %d", nodeID, resp.StatusCode)
	}
	return nil
}

func (c *Coordinator) forwardDelete(nodeID, tableName, key string) error {
	addr := c.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return fmt.Errorf("unknown node %s", nodeID)
	}

	url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, tableName, key)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("forward delete to %s: %w", nodeID, err)
	}
	resp.Body.Close()

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

	// Replication endpoint
	replicateHandler := func(w http.ResponseWriter, r *http.Request) {
		tableName := r.URL.Query().Get("table")
		key := r.URL.Query().Get("key")
		op := r.URL.Query().Get("op")

		if tableName == "" || key == "" {
			http.Error(w, "table and key required", http.StatusBadRequest)
			return
		}

		switch op {
		case "get":
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			doc, err := t.GetItem(key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"item": doc})

		case "put":
			body, _ := io.ReadAll(r.Body)
			var doc map[string]interface{}
			if err := json.Unmarshal(body, &doc); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if err := t.PutItem(key, doc); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)

		case "delete":
			t, err := c.tableManager.GetTable(tableName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if err := t.DeleteItem(key); err != nil {
				log.Printf("replicate delete %s/%s: %v", tableName, key, err)
			}
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "unknown op", http.StatusBadRequest)
		}
	}

	router.POST("/internal/v1/replicate", replicateHandler)
	router.GET("/internal/v1/replicate", replicateHandler)
	router.DELETE("/internal/v1/replicate", replicateHandler)
}
