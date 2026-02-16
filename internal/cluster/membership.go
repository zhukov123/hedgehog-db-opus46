package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

var ErrCannotRemoveSelf = errors.New("cannot remove self from cluster")

// NodeStatus represents the state of a cluster node.
type NodeStatus int

const (
	NodeAlive   NodeStatus = iota
	NodeSuspect
	NodeDead
)

func (s NodeStatus) String() string {
	switch s {
	case NodeAlive:
		return "alive"
	case NodeSuspect:
		return "suspect"
	case NodeDead:
		return "dead"
	default:
		return "unknown"
	}
}

// NodeInfo holds information about a cluster node.
type NodeInfo struct {
	ID       string     `json:"id"`
	Addr     string     `json:"addr"`
	Status   NodeStatus `json:"status"`
	LastSeen time.Time  `json:"last_seen"`
}

// Membership manages cluster membership via heartbeats.
type Membership struct {
	selfID   string
	selfAddr string
	nodes    map[string]*NodeInfo
	ring     *Ring
	mu       sync.RWMutex

	heartbeatInterval time.Duration
	failureTimeout    time.Duration
	stopCh            chan struct{}
	client            *http.Client

	onRecovery func(nodeID string) // called when a node transitions to Alive from Dead/Suspect
}

// NewMembership creates a membership manager.
func NewMembership(selfID, selfAddr string, ring *Ring, heartbeatInterval, failureTimeout time.Duration) *Membership {
	m := &Membership{
		selfID:            selfID,
		selfAddr:          selfAddr,
		nodes:             make(map[string]*NodeInfo),
		ring:              ring,
		heartbeatInterval: heartbeatInterval,
		failureTimeout:    failureTimeout,
		stopCh:            make(chan struct{}),
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
	}

	// Add self
	m.nodes[selfID] = &NodeInfo{
		ID:       selfID,
		Addr:     selfAddr,
		Status:   NodeAlive,
		LastSeen: time.Now(),
	}
	ring.AddNode(selfID)

	return m
}

// SetRecoveryCallback registers a function to be called (in a new goroutine)
// when a node transitions from Dead or Suspect back to Alive.
func (m *Membership) SetRecoveryCallback(fn func(nodeID string)) {
	m.onRecovery = fn
}

// Start begins the heartbeat loop.
func (m *Membership) Start() {
	go m.heartbeatLoop()
	go m.failureDetectionLoop()
}

// Stop stops the heartbeat loop.
func (m *Membership) Stop() {
	close(m.stopCh)
}

// AddNode adds a known node.
func (m *Membership) AddNode(id, addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.nodes[id]; exists {
		m.nodes[id].Addr = addr
		m.nodes[id].Status = NodeAlive
		m.nodes[id].LastSeen = time.Now()
		return
	}

	m.nodes[id] = &NodeInfo{
		ID:       id,
		Addr:     addr,
		Status:   NodeAlive,
		LastSeen: time.Now(),
	}
	m.ring.AddNode(id)
	log.Printf("Node %s (%s) joined the cluster", id, addr)
}

// RemoveNode removes a node from the cluster (membership and ring).
// Returns ErrCannotRemoveSelf if attempting to remove this node.
func (m *Membership) RemoveNode(nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if nodeID == m.selfID {
		return ErrCannotRemoveSelf
	}

	if _, exists := m.nodes[nodeID]; !exists {
		return nil
	}

	delete(m.nodes, nodeID)
	m.ring.RemoveNode(nodeID)
	log.Printf("Node %s removed from cluster", nodeID)
	return nil
}

// HandleHeartbeat processes an incoming heartbeat.
func (m *Membership) HandleHeartbeat(fromID, fromAddr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, exists := m.nodes[fromID]
	if !exists {
		// If we had this address as a seed (id "seed-<addr>"), remove it so we don't show duplicate entries
		seedID := "seed-" + fromAddr
		if seed, ok := m.nodes[seedID]; ok && seed.Addr == fromAddr {
			delete(m.nodes, seedID)
			m.ring.RemoveNode(seedID)
		}
		m.nodes[fromID] = &NodeInfo{
			ID:       fromID,
			Addr:     fromAddr,
			Status:   NodeAlive,
			LastSeen: time.Now(),
		}
		m.ring.AddNode(fromID)
		log.Printf("Discovered node %s (%s)", fromID, fromAddr)
		return
	}

	oldStatus := node.Status
	node.Status = NodeAlive
	node.LastSeen = time.Now()
	node.Addr = fromAddr

	if (oldStatus == NodeSuspect || oldStatus == NodeDead) && m.onRecovery != nil {
		log.Printf("Node %s recovered (was %s), triggering hint replay", fromID, oldStatus)
		if oldStatus == NodeDead {
			m.ring.AddNode(fromID)
		}
		go m.onRecovery(fromID)
	}
}

// GetAliveNodes returns all alive nodes.
func (m *Membership) GetAliveNodes() []*NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*NodeInfo, 0)
	for _, n := range m.nodes {
		if n.Status == NodeAlive {
			result = append(result, &NodeInfo{
				ID:       n.ID,
				Addr:     n.Addr,
				Status:   n.Status,
				LastSeen: n.LastSeen,
			})
		}
	}
	return result
}

// GetAllNodes returns all known nodes.
func (m *Membership) GetAllNodes() []*NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*NodeInfo, 0, len(m.nodes))
	for _, n := range m.nodes {
		result = append(result, &NodeInfo{
			ID:       n.ID,
			Addr:     n.Addr,
			Status:   n.Status,
			LastSeen: n.LastSeen,
		})
	}
	return result
}

// IsAlive checks if a node is alive.
func (m *Membership) IsAlive(nodeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[nodeID]
	if !ok {
		return false
	}
	return n.Status == NodeAlive
}

// GetNodeAddr returns the address of a node.
func (m *Membership) GetNodeAddr(nodeID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[nodeID]
	if !ok {
		return ""
	}
	return n.Addr
}

// SelfID returns this node's ID.
func (m *Membership) SelfID() string {
	return m.selfID
}

func (m *Membership) heartbeatLoop() {
	ticker := time.NewTicker(m.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sendHeartbeats()
		}
	}
}

func (m *Membership) sendHeartbeats() {
	m.mu.RLock()
	targets := make([]*NodeInfo, 0)
	for id, n := range m.nodes {
		if id != m.selfID && n.Status != NodeDead {
			targets = append(targets, n)
		}
	}
	m.mu.RUnlock()

	hb := map[string]string{
		"node_id": m.selfID,
		"addr":    m.selfAddr,
	}
	body, _ := json.Marshal(hb)

	for _, target := range targets {
		go func(addr string) {
			url := fmt.Sprintf("http://%s/internal/v1/heartbeat", addr)
			resp, err := m.client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				return
			}
			resp.Body.Close()
		}(target.Addr)
	}
}

func (m *Membership) failureDetectionLoop() {
	ticker := time.NewTicker(m.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.detectFailures()
		}
	}
}

func (m *Membership) detectFailures() {
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, n := range m.nodes {
		if id == m.selfID {
			n.LastSeen = now
			continue
		}

		elapsed := now.Sub(n.LastSeen)

		switch n.Status {
		case NodeAlive:
			if elapsed > m.failureTimeout {
				n.Status = NodeSuspect
				log.Printf("Node %s is suspect (no heartbeat for %s)", id, elapsed)
			}
		case NodeSuspect:
			if elapsed > m.failureTimeout*2 {
				n.Status = NodeDead
				m.ring.RemoveNode(id)
				log.Printf("Node %s declared dead", id)
			}
		}
	}
}
