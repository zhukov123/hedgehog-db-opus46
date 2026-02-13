package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// HintedHandoff stores writes for unavailable nodes.
type HintedHandoff struct {
	hints map[string][]hint // nodeID -> pending hints
	mu    sync.Mutex
}

type hint struct {
	TableName string                 `json:"table_name"`
	Key       string                 `json:"key"`
	Op        string                 `json:"op"` // "put" or "delete"
	Doc       map[string]interface{} `json:"doc,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewHintedHandoff creates a hinted handoff store.
func NewHintedHandoff() *HintedHandoff {
	return &HintedHandoff{
		hints: make(map[string][]hint),
	}
}

// AddHint stores a hint for a down node.
func (hh *HintedHandoff) AddHint(nodeID string, h hint) {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	hh.hints[nodeID] = append(hh.hints[nodeID], h)
}

// DrainHints returns and removes all hints for a node.
func (hh *HintedHandoff) DrainHints(nodeID string) []hint {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	hints := hh.hints[nodeID]
	delete(hh.hints, nodeID)
	return hints
}

// PendingCount returns the number of pending hints for a node.
func (hh *HintedHandoff) PendingCount(nodeID string) int {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	return len(hh.hints[nodeID])
}

// Replicator handles data replication to peer nodes.
type Replicator struct {
	membership *Membership
	handoff    *HintedHandoff
	client     *http.Client
}

// NewReplicator creates a replicator.
func NewReplicator(membership *Membership) *Replicator {
	return &Replicator{
		membership: membership,
		handoff:    NewHintedHandoff(),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// ReplicateWrite sends a write to all replica nodes (except self).
func (r *Replicator) ReplicateWrite(tableName, key string, doc map[string]interface{}, nodes []string) {
	selfID := r.membership.SelfID()
	for _, nodeID := range nodes {
		if nodeID == selfID {
			continue
		}

		if !r.membership.IsAlive(nodeID) {
			// Store hinted handoff
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "put",
				Doc:       doc,
				Timestamp: time.Now(),
			})
			log.Printf("Stored hint for down node %s: put %s/%s", nodeID, tableName, key)
			continue
		}

		addr := r.membership.GetNodeAddr(nodeID)
		if addr == "" {
			continue
		}

		body, _ := json.Marshal(doc)
		url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", addr, tableName, key)
		resp, err := r.client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("Replicate write to %s failed: %v", nodeID, err)
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "put",
				Doc:       doc,
				Timestamp: time.Now(),
			})
			continue
		}
		resp.Body.Close()
	}
}

// ReplicateDelete sends a delete to all replica nodes (except self).
func (r *Replicator) ReplicateDelete(tableName, key string, nodes []string) {
	selfID := r.membership.SelfID()
	for _, nodeID := range nodes {
		if nodeID == selfID {
			continue
		}

		if !r.membership.IsAlive(nodeID) {
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "delete",
				Timestamp: time.Now(),
			})
			continue
		}

		addr := r.membership.GetNodeAddr(nodeID)
		if addr == "" {
			continue
		}

		url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, tableName, key)
		req, _ := http.NewRequest(http.MethodDelete, url, nil)
		resp, err := r.client.Do(req)
		if err != nil {
			log.Printf("Replicate delete to %s failed: %v", nodeID, err)
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "delete",
				Timestamp: time.Now(),
			})
			continue
		}
		resp.Body.Close()
	}
}

// ReplayHints replays stored hints for a recovered node.
func (r *Replicator) ReplayHints(nodeID string) {
	hints := r.handoff.DrainHints(nodeID)
	if len(hints) == 0 {
		return
	}

	addr := r.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return
	}

	log.Printf("Replaying %d hints to recovered node %s", len(hints), nodeID)

	for _, h := range hints {
		switch h.Op {
		case "put":
			body, _ := json.Marshal(h.Doc)
			url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", addr, h.TableName, h.Key)
			resp, err := r.client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				log.Printf("Hint replay to %s failed: %v", nodeID, err)
				r.handoff.AddHint(nodeID, h)
				return
			}
			resp.Body.Close()

		case "delete":
			url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, h.TableName, h.Key)
			req, _ := http.NewRequest(http.MethodDelete, url, nil)
			resp, err := r.client.Do(req)
			if err != nil {
				log.Printf("Hint replay to %s failed: %v", nodeID, err)
				r.handoff.AddHint(nodeID, h)
				return
			}
			resp.Body.Close()
		}
	}
}

// Handoff returns the hinted handoff store.
func (r *Replicator) Handoff() *HintedHandoff {
	return r.handoff
}
