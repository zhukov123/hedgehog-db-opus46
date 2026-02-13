package cluster

import (
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
)

// VNode represents a virtual node on the consistent hash ring.
type VNode struct {
	Hash     uint32
	NodeID   string
	VNodeIdx int
}

// Ring implements consistent hashing with virtual nodes.
type Ring struct {
	vnodes       []VNode
	vnodeCount   int
	physicalNodes map[string]bool
	mu           sync.RWMutex
}

// NewRing creates a consistent hash ring.
func NewRing(vnodesPerNode int) *Ring {
	if vnodesPerNode <= 0 {
		vnodesPerNode = 128
	}
	return &Ring{
		vnodeCount:    vnodesPerNode,
		physicalNodes: make(map[string]bool),
	}
}

// AddNode adds a physical node with its virtual nodes to the ring.
func (r *Ring) AddNode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.physicalNodes[nodeID] {
		return
	}

	r.physicalNodes[nodeID] = true

	for i := 0; i < r.vnodeCount; i++ {
		hash := hashKey(fmt.Sprintf("%s#%d", nodeID, i))
		r.vnodes = append(r.vnodes, VNode{
			Hash:     hash,
			NodeID:   nodeID,
			VNodeIdx: i,
		})
	}

	sort.Slice(r.vnodes, func(i, j int) bool {
		return r.vnodes[i].Hash < r.vnodes[j].Hash
	})
}

// RemoveNode removes a physical node and all its virtual nodes.
func (r *Ring) RemoveNode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.physicalNodes[nodeID] {
		return
	}

	delete(r.physicalNodes, nodeID)

	filtered := make([]VNode, 0, len(r.vnodes))
	for _, vn := range r.vnodes {
		if vn.NodeID != nodeID {
			filtered = append(filtered, vn)
		}
	}
	r.vnodes = filtered
}

// GetNode returns the primary node responsible for a key.
func (r *Ring) GetNode(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return ""
	}

	hash := hashKey(key)
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].Hash >= hash
	})
	if idx == len(r.vnodes) {
		idx = 0
	}
	return r.vnodes[idx].NodeID
}

// GetNodes returns N distinct physical nodes responsible for a key.
// The first node is the coordinator; subsequent nodes are replicas.
func (r *Ring) GetNodes(key string, n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return nil
	}

	hash := hashKey(key)
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].Hash >= hash
	})
	if idx == len(r.vnodes) {
		idx = 0
	}

	seen := make(map[string]bool)
	result := make([]string, 0, n)

	for i := 0; i < len(r.vnodes) && len(result) < n; i++ {
		vnode := r.vnodes[(idx+i)%len(r.vnodes)]
		if !seen[vnode.NodeID] {
			seen[vnode.NodeID] = true
			result = append(result, vnode.NodeID)
		}
	}

	return result
}

// Nodes returns all physical nodes in the ring.
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]string, 0, len(r.physicalNodes))
	for n := range r.physicalNodes {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

// Size returns the number of physical nodes.
func (r *Ring) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.physicalNodes)
}

// VNodes returns all virtual nodes (for visualization).
func (r *Ring) VNodes() []VNode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]VNode, len(r.vnodes))
	copy(result, r.vnodes)
	return result
}

// hashKey computes FNV-1a hash of a key string.
func hashKey(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}
