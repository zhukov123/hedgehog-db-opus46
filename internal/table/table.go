package table

import (
	"bytes"
	"container/heap"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/hedgehog-db/hedgehog/internal/storage"
)

const (
	DefaultPartitionCount = 8
)

// Table wraps one or more B+ tree partitions and provides CRUD operations for JSON documents.
// When PartitionCount > 1, keys are routed by hash to partitions for parallel writes.
type Table struct {
	Name           string
	partitions     []*partition
	partitionCount int
	dataDir        string
	mu             sync.RWMutex
}

// partition is a single shard: one B+ tree with its own pager, pool, WAL, and lock.
type partition struct {
	tree     *storage.BPlusTree
	pool     *storage.BufferPool
	pager    *storage.Pager
	freelist *storage.FreeList
	wal      *storage.WAL
}

// TableOptions holds configuration for opening a table.
type TableOptions struct {
	DataDir         string
	BufferPoolSize  int
	PartitionCount  int // number of partitions; 1 = legacy single tree, >1 = hash-partitioned
}

// OpenTable opens or creates a table.
func OpenTable(name string, opts TableOptions) (*Table, error) {
	if opts.BufferPoolSize <= 0 {
		opts.BufferPoolSize = storage.DefaultBufferPoolSize
	}
	if opts.PartitionCount <= 0 {
		opts.PartitionCount = DefaultPartitionCount
	}

	// Detect layout: legacy single file or partitioned
	legacyPath := filepath.Join(opts.DataDir, name+".db")
	partition0Path := filepath.Join(opts.DataDir, fmt.Sprintf("%s_p0.db", name))

	usePartitions := opts.PartitionCount > 1
	if usePartitions {
		// If legacy exists, use it as single partition (backward compat)
		if fileExists(legacyPath) {
			usePartitions = false
			opts.PartitionCount = 1
		} else if !fileExists(partition0Path) {
			// Creating new partitioned table
		}
	}

	if !usePartitions || opts.PartitionCount == 1 {
		// Legacy single-partition layout
		p, err := openPartition(name, 0, opts, true)
		if err != nil {
			return nil, err
		}
		return &Table{
			Name:           name,
			partitions:     []*partition{p},
			partitionCount: 1,
			dataDir:        opts.DataDir,
		}, nil
	}

	// Partitioned layout
	partitions := make([]*partition, opts.PartitionCount)
	for i := 0; i < opts.PartitionCount; i++ {
		p, err := openPartition(name, i, opts, false)
		if err != nil {
			for j := 0; j < i; j++ {
				partitions[j].close()
			}
			return nil, err
		}
		partitions[i] = p
	}

	return &Table{
		Name:           name,
		partitions:     partitions,
		partitionCount: opts.PartitionCount,
		dataDir:        opts.DataDir,
	}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// openPartition opens one partition. legacy=true uses name.db; legacy=false uses name_p{i}.db.
func openPartition(name string, idx int, opts TableOptions, legacy bool) (*partition, error) {
	var dbPath, walPath string
	if legacy {
		dbPath = filepath.Join(opts.DataDir, name+".db")
		walPath = filepath.Join(opts.DataDir, name+".wal")
	} else {
		dbPath = filepath.Join(opts.DataDir, fmt.Sprintf("%s_p%d.db", name, idx))
		walPath = filepath.Join(opts.DataDir, fmt.Sprintf("%s_p%d.wal", name, idx))
	}

	pager, err := storage.OpenPager(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open partition %s[%d]: %w", name, idx, err)
	}

	freelist := storage.NewFreeList(pager)
	pool := storage.NewBufferPool(pager, freelist, opts.BufferPoolSize)

	wal, err := storage.OpenWAL(walPath)
	if err != nil {
		pager.Close()
		return nil, fmt.Errorf("open WAL for %s[%d]: %w", name, idx, err)
	}

	pageWrites, err := wal.Recover()
	if err != nil {
		wal.Close()
		pager.Close()
		return nil, fmt.Errorf("WAL recovery for %s[%d]: %w", name, idx, err)
	}

	for pageID, data := range pageWrites {
		page := &storage.Page{ID: pageID, Data: data}
		if err := pager.WritePage(page); err != nil {
			wal.Close()
			pager.Close()
			return nil, fmt.Errorf("apply WAL page %d: %w", pageID, err)
		}
	}
	if len(pageWrites) > 0 {
		pager.Sync()
		wal.Truncate()
	}

	rootPageID := pager.Header().RootPageID
	tree, err := storage.OpenBPlusTree(pool, pager, rootPageID)
	if err != nil {
		wal.Close()
		pager.Close()
		return nil, fmt.Errorf("open B+ tree for %s[%d]: %w", name, idx, err)
	}

	tree.SetWAL(wal)
	wal.EnableGroupCommit(storage.DefaultGroupCommitWindow)
	pool.StartFlushLoop()

	return &partition{tree: tree, pool: pool, pager: pager, freelist: freelist, wal: wal}, nil
}

func (p *partition) close() error {
	p.pool.StopFlushLoop()
	_ = p.tree.Close()
	_ = p.wal.Close()
	return p.pager.Close()
}

// partitionForKey returns the partition index for the given key.
func partitionForKey(key []byte, n int) int {
	if n <= 1 {
		return 0
	}
	h := fnv.New64a()
	h.Write(key)
	return int(h.Sum64() % uint64(n))
}

// GetItem retrieves a JSON document by key.
func (t *Table) GetItem(key string) (map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ek := EncodeKey(key)
	idx := partitionForKey(ek, t.partitionCount)
	data, err := t.partitions[idx].tree.Search(ek)
	if err != nil {
		return nil, err
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal item: %w", err)
	}

	return doc, nil
}

// PutItem stores a JSON document with the given key.
// Routes to a partition by hash; each partition has its own lock for parallel writes.
func (t *Table) PutItem(key string, doc map[string]interface{}) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal item: %w", err)
	}

	ek := EncodeKey(key)
	idx := partitionForKey(ek, t.partitionCount)
	return t.partitions[idx].tree.Insert(ek, data)
}

// DeleteItem removes a document by key.
func (t *Table) DeleteItem(key string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ek := EncodeKey(key)
	idx := partitionForKey(ek, t.partitionCount)
	return t.partitions[idx].tree.Delete(ek)
}

// Scan iterates over all items in sorted key order (merged across partitions).
func (t *Table) Scan(fn func(key string, doc map[string]interface{}) bool) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.partitionCount == 1 {
		return t.partitions[0].tree.Scan(func(k, v []byte) bool {
			var doc map[string]interface{}
			if err := json.Unmarshal(v, &doc); err != nil {
				return true
			}
			return fn(DecodeKey(k), doc)
		})
	}
	return t.scanMerged(func(k, v []byte) bool {
		var doc map[string]interface{}
		if err := json.Unmarshal(v, &doc); err != nil {
			return true
		}
		return fn(DecodeKey(k), doc)
	})
}

// ScanChunked iterates over all items, releasing locks every chunkSize entries.
func (t *Table) ScanChunked(chunkSize int, fn func(key string, doc map[string]interface{}) bool) error {
	if chunkSize <= 0 {
		chunkSize = 500
	}
	if t.partitionCount == 1 {
		return t.partitions[0].tree.ScanChunked(chunkSize, func(k, v []byte) bool {
			var doc map[string]interface{}
			if err := json.Unmarshal(v, &doc); err != nil {
				return true
			}
			return fn(DecodeKey(k), doc)
		})
	}

	var lastKey []byte
	for {
		done, err := t.scanMergedChunk(chunkSize, lastKey, func(k, v []byte) bool {
			lastKey = make([]byte, len(k))
			copy(lastKey, k)
			var doc map[string]interface{}
			if err := json.Unmarshal(v, &doc); err != nil {
				return true
			}
			return fn(DecodeKey(k), doc)
		})
		if err != nil || done {
			return err
		}
		runtime.Gosched()
	}
}

// scanMerged merges sorted scans from all partitions and invokes fn for each (key, value).
func (t *Table) scanMerged(fn func(k, v []byte) bool) error {
	// Each partition sends (key, value) on its channel in sorted order
	type kv struct {
		k, v []byte
		i    int
	}
	chans := make([]chan kv, t.partitionCount)
	for i := 0; i < t.partitionCount; i++ {
		ch := make(chan kv, 64)
		chans[i] = ch
		idx := i
		go func() {
			t.partitions[idx].tree.Scan(func(k, v []byte) bool {
				ch <- kv{k: k, v: v, i: idx}
				return true
			})
			close(ch)
		}()
	}

	// Min-heap of (key, value, partitionIdx) for merge
	h := &mergeHeap{items: make([]mergeItem, 0, t.partitionCount)}

	// Prime heap with first from each partition
	for i := 0; i < t.partitionCount; i++ {
		x, ok := <-chans[i]
		if ok {
			heap.Push(h, mergeItem{k: x.k, v: x.v, i: x.i})
		}
	}

	for h.Len() > 0 {
		top := heap.Pop(h).(mergeItem)
		if !fn(top.k, top.v) {
			return nil
		}
		// Refill from that partition
		if x, ok := <-chans[top.i]; ok {
			heap.Push(h, mergeItem{k: x.k, v: x.v, i: x.i})
		}
	}
	return nil
}

// scanMergedChunk scans one chunk starting after afterKey, returns (done, err).
func (t *Table) scanMergedChunk(chunkSize int, afterKey []byte, fn func(k, v []byte) bool) (bool, error) {
	// For partitioned scan we do a full merge but stop after chunkSize
	count := 0
	done := false
	err := t.scanMerged(func(k, v []byte) bool {
		if afterKey != nil && bytes.Compare(k, afterKey) <= 0 {
			return true
		}
		afterKey = nil
		if !fn(k, v) {
			done = true
			return false
		}
		count++
		return count < chunkSize
	})
	if err != nil {
		return true, err
	}
	return done || count < chunkSize, nil
}

// mergeHeap implements heap.Interface for merge by key.
type mergeItem struct {
	k, v []byte
	i    int
}

type mergeHeap struct {
	items []mergeItem
}

func (h *mergeHeap) Len() int           { return len(h.items) }
func (h *mergeHeap) Less(i, j int) bool { return bytes.Compare(h.items[i].k, h.items[j].k) < 0 }
func (h *mergeHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *mergeHeap) Push(x any)         { h.items = append(h.items, x.(mergeItem)) }
func (h *mergeHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// Count returns the number of items in the table.
func (t *Table) Count() (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sum := 0
	for _, p := range t.partitions {
		c, err := p.tree.Count()
		if err != nil {
			return 0, err
		}
		sum += c
	}
	return sum, nil
}

// Close stops the flush loop, flushes all remaining dirty pages, and closes the table.
func (t *Table) Close() error {
	var lastErr error
	for _, p := range t.partitions {
		if err := p.close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Tree returns the underlying B+ tree for partition 0 (for testing/internal use).
// Use PartitionTree(i) for a specific partition.
func (t *Table) Tree() *storage.BPlusTree {
	return t.partitions[0].tree
}

// PartitionTree returns the B+ tree for partition i.
func (t *Table) PartitionTree(i int) *storage.BPlusTree {
	if i < 0 || i >= len(t.partitions) {
		return nil
	}
	return t.partitions[i].tree
}
