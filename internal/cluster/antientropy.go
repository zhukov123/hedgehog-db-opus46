package cluster

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hedgehog-db/hedgehog/internal/table"
)

type merkleKV struct {
	key   string
	value []byte
}

// MerkleNode represents a node in a Merkle tree.
type MerkleNode struct {
	Hash     string      `json:"hash"`
	KeyRange [2]string   `json:"key_range"` // [start, end]
	Left     *MerkleNode `json:"left,omitempty"`
	Right    *MerkleNode `json:"right,omitempty"`
	IsLeaf   bool        `json:"is_leaf"`
	Keys     []string    `json:"keys,omitempty"` // only for leaf nodes
}

// BuildMerkleTree builds a Merkle tree over the keys in a table.
func BuildMerkleTree(t *table.Table) (*MerkleNode, error) {
	var items []merkleKV
	err := t.Scan(func(key string, doc map[string]interface{}) bool {
		data, _ := json.Marshal(doc)
		items = append(items, merkleKV{key: key, value: data})
		return true
	})
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return &MerkleNode{
			Hash:   hashBytes(nil),
			IsLeaf: true,
		}, nil
	}

	return buildMerkleRecursive(items, 0, len(items)-1), nil
}

func buildMerkleRecursive(items []merkleKV, lo, hi int) *MerkleNode {
	if hi-lo < 16 {
		// Leaf: hash all key-value pairs
		h := sha256.New()
		keys := make([]string, 0, hi-lo+1)
		for i := lo; i <= hi; i++ {
			h.Write([]byte(items[i].key))
			h.Write(items[i].value)
			keys = append(keys, items[i].key)
		}
		return &MerkleNode{
			Hash:     hex.EncodeToString(h.Sum(nil)),
			KeyRange: [2]string{items[lo].key, items[hi].key},
			IsLeaf:   true,
			Keys:     keys,
		}
	}

	mid := (lo + hi) / 2
	left := buildMerkleRecursive(items, lo, mid)
	right := buildMerkleRecursive(items, mid+1, hi)

	h := sha256.New()
	h.Write([]byte(left.Hash))
	h.Write([]byte(right.Hash))

	return &MerkleNode{
		Hash:     hex.EncodeToString(h.Sum(nil)),
		KeyRange: [2]string{items[lo].key, items[hi].key},
		Left:     left,
		Right:    right,
	}
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// MerkleTreeDigest is a compact representation for exchange.
type MerkleTreeDigest struct {
	TableName string `json:"table_name"`
	RootHash  string `json:"root_hash"`
	NodeCount int    `json:"node_count"`
}

// AntiEntropy performs periodic Merkle tree comparison and repair.
type AntiEntropy struct {
	membership   *Membership
	ring         *Ring
	tableManager *table.TableManager
	interval     time.Duration
	stopCh       chan struct{}
	client       *http.Client
	mu           sync.Mutex
}

// NewAntiEntropy creates an anti-entropy service.
func NewAntiEntropy(membership *Membership, ring *Ring, tm *table.TableManager, interval time.Duration) *AntiEntropy {
	return &AntiEntropy{
		membership:   membership,
		ring:         ring,
		tableManager: tm,
		interval:     interval,
		stopCh:       make(chan struct{}),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Start begins the periodic anti-entropy process.
func (ae *AntiEntropy) Start() {
	go ae.loop()
}

// Stop stops the anti-entropy process.
func (ae *AntiEntropy) Stop() {
	close(ae.stopCh)
}

func (ae *AntiEntropy) loop() {
	ticker := time.NewTicker(ae.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ae.stopCh:
			return
		case <-ticker.C:
			ae.runSync()
		}
	}
}

func (ae *AntiEntropy) runSync() {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	tables := ae.tableManager.Catalog().ListTables()
	nodes := ae.membership.GetAliveNodes()
	selfID := ae.membership.SelfID()

	for _, tableMeta := range tables {
		for _, node := range nodes {
			if node.ID == selfID {
				continue
			}

			if err := ae.syncTableWithNode(tableMeta.Name, node); err != nil {
				log.Printf("Anti-entropy sync %s with %s failed: %v", tableMeta.Name, node.ID, err)
			}
		}
	}
}

func (ae *AntiEntropy) syncTableWithNode(tableName string, node *NodeInfo) error {
	// Get local table data
	t, err := ae.tableManager.GetTable(tableName)
	if err != nil {
		return err
	}

	// Collect local keys and values
	localData := make(map[string][]byte)
	err = t.Scan(func(key string, doc map[string]interface{}) bool {
		data, _ := json.Marshal(doc)
		localData[key] = data
		return true
	})
	if err != nil {
		return err
	}

	// Get remote keys
	url := fmt.Sprintf("http://%s/internal/v1/anti-entropy?table=%s", node.Addr, tableName)
	resp, err := ae.client.Get(url)
	if err != nil {
		return fmt.Errorf("fetch remote keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote returned status %d", resp.StatusCode)
	}

	var remoteKeys struct {
		Keys map[string]string `json:"keys"` // key -> hash
	}
	if err := json.NewDecoder(resp.Body).Decode(&remoteKeys); err != nil {
		return err
	}

	// Find divergent keys
	var keysToSend []string
	for key, data := range localData {
		localHash := hashBytes(data)
		if remoteHash, ok := remoteKeys.Keys[key]; !ok || remoteHash != localHash {
			keysToSend = append(keysToSend, key)
		}
	}

	// Send missing/divergent keys to remote
	sort.Strings(keysToSend)
	for _, key := range keysToSend {
		data := localData[key]
		sendURL := fmt.Sprintf("http://%s/internal/v1/replicate?table=%s&key=%s&op=put", node.Addr, tableName, key)
		resp, err := ae.client.Post(sendURL, "application/json", bytes.NewReader(data))
		if err != nil {
			log.Printf("Anti-entropy send %s/%s to %s failed: %v", tableName, key, node.ID, err)
			continue
		}
		resp.Body.Close()
	}

	if len(keysToSend) > 0 {
		log.Printf("Anti-entropy: synced %d keys in table %s with node %s", len(keysToSend), tableName, node.ID)
	}

	return nil
}

// RegisterRoutes registers anti-entropy HTTP endpoints.
func (ae *AntiEntropy) RegisterRoutes(router interface {
	GET(pattern string, handler http.HandlerFunc)
}) {
	router.GET("/internal/v1/anti-entropy", func(w http.ResponseWriter, r *http.Request) {
		tableName := r.URL.Query().Get("table")
		if tableName == "" {
			http.Error(w, "table required", http.StatusBadRequest)
			return
		}

		t, err := ae.tableManager.GetTable(tableName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		keys := make(map[string]string)
		err = t.Scan(func(key string, doc map[string]interface{}) bool {
			data, _ := json.Marshal(doc)
			keys[key] = hashBytes(data)
			return true
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": keys,
		})
	})
}
