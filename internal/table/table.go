package table

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/hedgehog-db/hedgehog/internal/storage"
)

// Table wraps a B+ tree and provides CRUD operations for JSON documents.
type Table struct {
	Name     string
	tree     *storage.BPlusTree
	pool     *storage.BufferPool
	pager    *storage.Pager
	freelist *storage.FreeList
	wal      *storage.WAL
	dataDir  string
	mu       sync.RWMutex
}

// TableOptions holds configuration for opening a table.
type TableOptions struct {
	DataDir        string
	BufferPoolSize int
}

// OpenTable opens or creates a table.
func OpenTable(name string, opts TableOptions) (*Table, error) {
	if opts.BufferPoolSize <= 0 {
		opts.BufferPoolSize = storage.DefaultBufferPoolSize
	}

	dbPath := filepath.Join(opts.DataDir, name+".db")
	walPath := filepath.Join(opts.DataDir, name+".wal")

	pager, err := storage.OpenPager(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open table %s: %w", name, err)
	}

	freelist := storage.NewFreeList(pager)
	pool := storage.NewBufferPool(pager, freelist, opts.BufferPoolSize)

	// Open WAL and recover if needed
	wal, err := storage.OpenWAL(walPath)
	if err != nil {
		pager.Close()
		return nil, fmt.Errorf("open WAL for table %s: %w", name, err)
	}

	// Recover from WAL
	pageWrites, err := wal.Recover()
	if err != nil {
		wal.Close()
		pager.Close()
		return nil, fmt.Errorf("WAL recovery for table %s: %w", name, err)
	}

	// Apply recovered page writes
	for pageID, data := range pageWrites {
		page := &storage.Page{
			ID:   pageID,
			Data: data,
		}
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
		return nil, fmt.Errorf("open B+ tree for table %s: %w", name, err)
	}

	return &Table{
		Name:     name,
		tree:     tree,
		pool:     pool,
		pager:    pager,
		freelist: freelist,
		wal:      wal,
		dataDir:  opts.DataDir,
	}, nil
}

// GetItem retrieves a JSON document by key.
func (t *Table) GetItem(key string) (map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	data, err := t.tree.Search(EncodeKey(key))
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
func (t *Table) PutItem(key string, doc map[string]interface{}) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal item: %w", err)
	}

	return t.tree.Insert(EncodeKey(key), data)
}

// DeleteItem removes a document by key.
func (t *Table) DeleteItem(key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.tree.Delete(EncodeKey(key))
}

// Scan iterates over all items in sorted key order.
func (t *Table) Scan(fn func(key string, doc map[string]interface{}) bool) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.tree.Scan(func(k, v []byte) bool {
		var doc map[string]interface{}
		if err := json.Unmarshal(v, &doc); err != nil {
			return true // skip corrupt entries
		}
		return fn(DecodeKey(k), doc)
	})
}

// Count returns the number of items in the table.
func (t *Table) Count() (int, error) {
	return t.tree.Count()
}

// Close flushes and closes the table.
func (t *Table) Close() error {
	if err := t.tree.Close(); err != nil {
		return err
	}
	if err := t.wal.Close(); err != nil {
		return err
	}
	return t.pager.Close()
}

// Tree returns the underlying B+ tree (for testing/internal use).
func (t *Table) Tree() *storage.BPlusTree {
	return t.tree
}
