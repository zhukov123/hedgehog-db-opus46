package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/hedgehog-db/hedgehog/internal/metrics"
)

// drainBody fully reads and closes a response body so the TCP connection
// can be returned to the pool for reuse.
func drainBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

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

// DrainN returns and removes up to n hints for a node.
// If there are more, the remainder stays in the queue.
func (hh *HintedHandoff) DrainN(nodeID string, n int) []hint {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	all := hh.hints[nodeID]
	if len(all) == 0 {
		return nil
	}
	if n >= len(all) {
		delete(hh.hints, nodeID)
		return all
	}
	batch := make([]hint, n)
	copy(batch, all[:n])
	hh.hints[nodeID] = all[n:]
	return batch
}

// NodeIDs returns all node IDs that have pending hints.
func (hh *HintedHandoff) NodeIDs() []string {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	ids := make([]string, 0, len(hh.hints))
	for id, h := range hh.hints {
		if len(h) > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// PendingCount returns the number of pending hints for a node.
func (hh *HintedHandoff) PendingCount(nodeID string) int {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	return len(hh.hints[nodeID])
}

// HintSummary is a read-only summary of a pending hint for API exposure.
type HintSummary struct {
	TableName string    `json:"table_name"`
	Key       string    `json:"key"`
	Op        string    `json:"op"`
	Timestamp time.Time `json:"timestamp"`
}

// PendingCounts returns the number of pending hints per node.
func (hh *HintedHandoff) PendingCounts() map[string]int {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	result := make(map[string]int)
	for nodeID, hints := range hh.hints {
		result[nodeID] = len(hints)
	}
	return result
}

// PendingHints returns hint summaries per node for API exposure.
func (hh *HintedHandoff) PendingHints() map[string][]HintSummary {
	hh.mu.Lock()
	defer hh.mu.Unlock()
	result := make(map[string][]HintSummary)
	for nodeID, hints := range hh.hints {
		summaries := make([]HintSummary, 0, len(hints))
		for _, h := range hints {
			summaries = append(summaries, HintSummary{
				TableName: h.TableName,
				Key:       h.Key,
				Op:        h.Op,
				Timestamp: h.Timestamp,
			})
		}
		result[nodeID] = summaries
	}
	return result
}

// Replicator handles data replication to peer nodes.
type Replicator struct {
	membership *Membership
	handoff    *HintedHandoff
	client     *http.Client
	stopCh     chan struct{}
	replayDone chan struct{}
}

// NewReplicator creates a replicator.
func NewReplicator(membership *Membership) *Replicator {
	return &Replicator{
		membership: membership,
		handoff:    NewHintedHandoff(),
		stopCh:     make(chan struct{}),
		replayDone: make(chan struct{}),
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

const replayInterval = 30 * time.Second
const replayBatchSize = 500

// StartReplayLoop starts a background goroutine that periodically drains
// pending hints for all alive nodes.
func (r *Replicator) StartReplayLoop() {
	go r.replayLoop()
}

// StopReplayLoop signals the replay loop to stop and waits for it to finish.
func (r *Replicator) StopReplayLoop() {
	close(r.stopCh)
	<-r.replayDone
}

func (r *Replicator) replayLoop() {
	defer close(r.replayDone)
	ticker := time.NewTicker(replayInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.replayAllPending()
		}
	}
}

func (r *Replicator) replayAllPending() {
	nodeIDs := r.handoff.NodeIDs()
	for _, nodeID := range nodeIDs {
		if !r.membership.IsAlive(nodeID) {
			continue
		}
		r.ReplayHintsBatch(nodeID, replayBatchSize)
	}
}

// ReplayHintsBatch replays up to n stored hints for a node.
func (r *Replicator) ReplayHintsBatch(nodeID string, n int) {
	hints := r.handoff.DrainN(nodeID, n)
	if len(hints) == 0 {
		return
	}

	addr := r.membership.GetNodeAddr(nodeID)
	if addr == "" {
		return
	}

	log.Printf("Replaying %d hints to node %s (%d may remain)", len(hints), nodeID, r.handoff.PendingCount(nodeID))

	for _, h := range hints {
		switch h.Op {
		case "put":
			body, _ := json.Marshal(h.Doc)
			url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", addr, h.TableName, h.Key)
			resp, err := r.client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				log.Printf("Hint replay to %s failed: %v", nodeID, err)
				metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "error").Inc()
				r.handoff.AddHint(nodeID, h)
				metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
				return
			}
			drainBody(resp)
			metrics.ReplicationHintReplayOpsTotal.WithLabelValues(nodeID, "put").Inc()

		case "delete":
			url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, h.TableName, h.Key)
			req, _ := http.NewRequest(http.MethodDelete, url, nil)
			resp, err := r.client.Do(req)
			if err != nil {
				log.Printf("Hint replay to %s failed: %v", nodeID, err)
				metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "error").Inc()
				r.handoff.AddHint(nodeID, h)
				metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
				return
			}
			drainBody(resp)
			metrics.ReplicationHintReplayOpsTotal.WithLabelValues(nodeID, "delete").Inc()
		}
	}

	metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "success").Inc()
	metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
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
			metrics.ReplicationSendTotal.WithLabelValues("put", nodeID, "hinted").Inc()
			metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
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
			metrics.ReplicationSendTotal.WithLabelValues("put", nodeID, "error").Inc()
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "put",
				Doc:       doc,
				Timestamp: time.Now(),
			})
			metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
			continue
		}
		drainBody(resp)
		metrics.ReplicationSendTotal.WithLabelValues("put", nodeID, "success").Inc()
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
			metrics.ReplicationSendTotal.WithLabelValues("delete", nodeID, "hinted").Inc()
			metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
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
			metrics.ReplicationSendTotal.WithLabelValues("delete", nodeID, "error").Inc()
			r.handoff.AddHint(nodeID, hint{
				TableName: tableName,
				Key:       key,
				Op:        "delete",
				Timestamp: time.Now(),
			})
			metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
			continue
		}
		drainBody(resp)
		metrics.ReplicationSendTotal.WithLabelValues("delete", nodeID, "success").Inc()
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
				metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "error").Inc()
				r.handoff.AddHint(nodeID, h)
				metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
				return
			}
			drainBody(resp)
			metrics.ReplicationHintReplayOpsTotal.WithLabelValues(nodeID, "put").Inc()

		case "delete":
			url := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=delete", addr, h.TableName, h.Key)
			req, _ := http.NewRequest(http.MethodDelete, url, nil)
			resp, err := r.client.Do(req)
			if err != nil {
				log.Printf("Hint replay to %s failed: %v", nodeID, err)
				metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "error").Inc()
				r.handoff.AddHint(nodeID, h)
				metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
				return
			}
			drainBody(resp)
			metrics.ReplicationHintReplayOpsTotal.WithLabelValues(nodeID, "delete").Inc()
		}
	}

	metrics.ReplicationHintReplayTotal.WithLabelValues(nodeID, "success").Inc()
	metrics.UpdateHintedHandoffGauges(r.handoff.PendingCounts())
}

// Handoff returns the hinted handoff store.
func (r *Replicator) Handoff() *HintedHandoff {
	return r.handoff
}
