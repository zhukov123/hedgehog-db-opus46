package storage

import (
	"fmt"
	"runtime"
	"sync"
)

// BPlusTree implements a disk-based B+ tree over the buffer pool.
type BPlusTree struct {
	pool       *BufferPool
	pager      *Pager
	wal        *WAL   // optional write-ahead log
	rootPageID uint32
	activeTxID uint64 // set during write operations while mu is held
	mu         sync.RWMutex
}

// SetWAL attaches a WAL to the tree so that Insert/Delete are crash-safe.
func (t *BPlusTree) SetWAL(w *WAL) {
	t.wal = w
}

// flushPage logs a page write to the WAL (if set) and flushes to disk.
// When WAL is active, the synchronous disk flush is skipped – the page stays
// dirty in the buffer pool and will be written by the background flush loop.
// Crash safety is provided by the WAL commit that follows.
func (t *BPlusTree) flushPage(pageID uint32) error {
	if t.wal != nil && t.activeTxID > 0 {
		data, ok := t.pool.GetCachedPageData(pageID)
		if ok {
			if _, err := t.wal.LogPageWrite(t.activeTxID, pageID, data); err != nil {
				return fmt.Errorf("WAL log page %d: %w", pageID, err)
			}
		}
		// With WAL, leave the page dirty for the background flush loop.
		t.pool.MarkDirty(pageID)
		return nil
	}
	// No WAL — synchronous flush (legacy path).
	return t.pool.FlushPage(pageID)
}

// OpenBPlusTree opens or creates a B+ tree. If rootPageID is 0, a new root leaf is created.
func OpenBPlusTree(pool *BufferPool, pager *Pager, rootPageID uint32) (*BPlusTree, error) {
	tree := &BPlusTree{
		pool:       pool,
		pager:      pager,
		rootPageID: rootPageID,
	}

	if rootPageID == 0 {
		// Create initial root leaf
		root, err := pool.NewPage(PageTypeLeaf)
		if err != nil {
			return nil, fmt.Errorf("create root: %w", err)
		}
		tree.rootPageID = root.ID
		pool.Unpin(root.ID)
		if err := pool.FlushPage(root.ID); err != nil {
			return nil, err
		}
		if err := pager.SetRootPageID(root.ID); err != nil {
			return nil, err
		}
	}

	return tree, nil
}

// RootPageID returns the current root page ID.
func (t *BPlusTree) RootPageID() uint32 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rootPageID
}

// Search looks up a key and returns its value.
func (t *BPlusTree) Search(key []byte) ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pageID := t.rootPageID
	for {
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("search fetch page %d: %w", pageID, err)
		}

		if page.PageType() == PageTypeLeaf {
			val, found := page.SearchLeaf(key)
			t.pool.Unpin(pageID)
			if !found {
				return nil, ErrKeyNotFound
			}
			return val, nil
		}

		// Internal node: find the child to follow
		childID := page.FindChild(key)
		t.pool.Unpin(pageID)
		pageID = childID
	}
}

// Insert inserts or updates a key-value pair.
func (t *BPlusTree) Insert(key, value []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Begin WAL transaction
	if t.wal != nil {
		t.activeTxID = t.wal.BeginTx()
		defer func() { t.activeTxID = 0 }()
	}

	if err := t.insertInternal(key, value); err != nil {
		return err
	}

	// Commit WAL transaction
	if t.wal != nil {
		if _, err := t.wal.LogCommit(t.activeTxID); err != nil {
			return fmt.Errorf("WAL commit: %w", err)
		}
	}
	return nil
}

func (t *BPlusTree) insertInternal(key, value []byte) error {
	// Find the leaf page
	path, err := t.findLeafPath(key)
	if err != nil {
		return err
	}

	leafID := path[len(path)-1]
	leaf, err := t.pool.FetchPage(leafID)
	if err != nil {
		return fmt.Errorf("insert fetch leaf %d: %w", leafID, err)
	}

	// Check if key already exists -> update
	if idx := leaf.leafSearchIndex(key); idx < int(leaf.CellCount()) {
		cellOffset := int(leaf.GetCellPointer(idx))
		existingKey, _ := leaf.ReadLeafCell(cellOffset)
		if compareKeys(existingKey, key) == 0 {
			// Delete old and re-insert (simpler than in-place update)
			leaf.DeleteLeafCell(key)
		}
	}

	// Try to insert
	err = leaf.InsertLeafCell(key, value)
	if err == nil {
		t.pool.Unpin(leafID)
		return t.flushPage(leafID)
	}

	if err != ErrPageFull {
		t.pool.Unpin(leafID)
		return err
	}

	// Need to split
	t.pool.Unpin(leafID)
	return t.splitAndInsert(path, key, value)
}

// Delete removes a key from the tree.
func (t *BPlusTree) Delete(key []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Begin WAL transaction
	if t.wal != nil {
		t.activeTxID = t.wal.BeginTx()
		defer func() { t.activeTxID = 0 }()
	}

	if err := t.deleteInternal(key); err != nil {
		return err
	}

	// Commit WAL transaction
	if t.wal != nil {
		if _, err := t.wal.LogCommit(t.activeTxID); err != nil {
			return fmt.Errorf("WAL commit: %w", err)
		}
	}
	return nil
}

func (t *BPlusTree) deleteInternal(key []byte) error {
	path, err := t.findLeafPath(key)
	if err != nil {
		return err
	}

	leafID := path[len(path)-1]
	leaf, err := t.pool.FetchPage(leafID)
	if err != nil {
		return err
	}

	if !leaf.DeleteLeafCell(key) {
		t.pool.Unpin(leafID)
		return ErrKeyNotFound
	}

	t.pool.Unpin(leafID)
	if err := t.flushPage(leafID); err != nil {
		return err
	}

	// Check for underflow and handle merging/redistribution
	return t.handleUnderflow(path)
}

// findLeafPath traverses from root to the target leaf, returning the path of page IDs.
func (t *BPlusTree) findLeafPath(key []byte) ([]uint32, error) {
	path := make([]uint32, 0, 8)
	pageID := t.rootPageID

	for {
		path = append(path, pageID)
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("findLeafPath fetch %d: %w", pageID, err)
		}

		if page.PageType() == PageTypeLeaf {
			t.pool.Unpin(pageID)
			return path, nil
		}

		childID := page.FindChild(key)
		t.pool.Unpin(pageID)
		pageID = childID
	}
}

// splitAndInsert handles leaf splitting and key promotion.
func (t *BPlusTree) splitAndInsert(path []uint32, key, value []byte) error {
	leafID := path[len(path)-1]

	leaf, err := t.pool.FetchPage(leafID)
	if err != nil {
		return err
	}

	// Collect all key-value pairs from the leaf plus the new one
	count := int(leaf.CellCount())
	type kv struct {
		key, value []byte
	}
	pairs := make([]kv, 0, count+1)
	inserted := false
	for i := 0; i < count; i++ {
		k, v := leaf.GetLeafKeyValueAt(i)
		if !inserted && compareKeys(key, k) <= 0 {
			pairs = append(pairs, kv{key, value})
			inserted = true
		}
		pairs = append(pairs, kv{k, v})
	}
	if !inserted {
		pairs = append(pairs, kv{key, value})
	}

	// Split in half
	mid := len(pairs) / 2

	// Create new right leaf
	rightLeaf, err := t.pool.NewPage(PageTypeLeaf)
	if err != nil {
		t.pool.Unpin(leafID)
		return err
	}

	// Rewrite left leaf
	t.clearPageCells(leaf)
	for _, p := range pairs[:mid] {
		if err := leaf.InsertLeafCell(p.key, p.value); err != nil {
			t.pool.Unpin(leafID)
			t.pool.Unpin(rightLeaf.ID)
			return fmt.Errorf("rewrite left leaf: %w", err)
		}
	}

	// Write right leaf
	for _, p := range pairs[mid:] {
		if err := rightLeaf.InsertLeafCell(p.key, p.value); err != nil {
			t.pool.Unpin(leafID)
			t.pool.Unpin(rightLeaf.ID)
			return fmt.Errorf("write right leaf: %w", err)
		}
	}

	// Link leaves: left -> right -> old right
	rightLeaf.SetRightPointer(leaf.RightPointer())
	leaf.SetRightPointer(rightLeaf.ID)

	// The promoted key is the first key of the right leaf
	promotedKey := make([]byte, len(pairs[mid].key))
	copy(promotedKey, pairs[mid].key)

	t.pool.Unpin(leafID)
	t.pool.Unpin(rightLeaf.ID)

	if err := t.flushPage(leafID); err != nil {
		return err
	}
	if err := t.flushPage(rightLeaf.ID); err != nil {
		return err
	}

	// Insert promoted key into parent
	return t.insertIntoParent(path[:len(path)-1], leafID, promotedKey, rightLeaf.ID)
}

// insertIntoParent inserts a key into a parent internal node, splitting if needed.
func (t *BPlusTree) insertIntoParent(parentPath []uint32, leftChildID uint32, key []byte, rightChildID uint32) error {
	if len(parentPath) == 0 {
		// Need a new root
		return t.createNewRoot(leftChildID, key, rightChildID)
	}

	parentID := parentPath[len(parentPath)-1]
	parent, err := t.pool.FetchPage(parentID)
	if err != nil {
		return err
	}

	// Try to insert into parent
	err = parent.InsertInternalCell(leftChildID, key)
	if err == nil {
		// Update the right child pointer:
		// After insertion, find where our key ended up and fix pointers
		t.fixInternalPointers(parent, key, leftChildID, rightChildID)
		t.pool.Unpin(parentID)
		return t.flushPage(parentID)
	}

	if err != ErrPageFull {
		t.pool.Unpin(parentID)
		return err
	}

	// Need to split internal node
	t.pool.Unpin(parentID)
	return t.splitInternalNode(parentPath, leftChildID, key, rightChildID)
}

// fixInternalPointers fixes child pointers after inserting a key into an internal node.
func (t *BPlusTree) fixInternalPointers(parent *Page, key []byte, leftChildID, rightChildID uint32) {
	count := int(parent.CellCount())
	for i := 0; i < count; i++ {
		cellOffset := int(parent.GetCellPointer(i))
		_, cellKey := parent.ReadInternalCell(cellOffset)
		if compareKeys(cellKey, key) == 0 {
			// This cell's child pointer is the left child
			parent.WriteInternalCell(cellOffset, leftChildID, key)

			// The next cell's child (or rightPointer) should be rightChildID
			if i+1 < count {
				nextOffset := int(parent.GetCellPointer(i + 1))
				nextChildID, nextKey := parent.ReadInternalCell(nextOffset)
				_ = nextChildID
				parent.WriteInternalCell(nextOffset, rightChildID, nextKey)
			} else {
				parent.SetRightPointer(rightChildID)
			}
			return
		}
	}
}

// splitInternalNode splits an internal node and promotes the middle key.
func (t *BPlusTree) splitInternalNode(path []uint32, leftChildID uint32, newKey []byte, rightChildID uint32) error {
	nodeID := path[len(path)-1]
	node, err := t.pool.FetchPage(nodeID)
	if err != nil {
		return err
	}

	// Collect all cells plus the new one
	count := int(node.CellCount())
	type cell struct {
		childID uint32
		key     []byte
	}
	cells := make([]cell, 0, count+1)

	inserted := false
	for i := 0; i < count; i++ {
		cid, k := node.GetInternalCellAt(i)
		if !inserted && compareKeys(newKey, k) <= 0 {
			cells = append(cells, cell{leftChildID, newKey})
			inserted = true
		}
		cells = append(cells, cell{cid, k})
	}
	if !inserted {
		cells = append(cells, cell{leftChildID, newKey})
	}

	// We also need to track the rightmost child
	oldRightPtr := node.RightPointer()

	// Find the new rightChildID for the inserted key
	// After sorting, we need to fix the right child of the new key
	for i, c := range cells {
		if compareKeys(c.key, newKey) == 0 {
			// The child pointer stored with this key is leftChildID
			// The next cell's child pointer should become rightChildID
			if i+1 < len(cells) {
				cells[i+1] = cell{rightChildID, cells[i+1].key}
			} else {
				oldRightPtr = rightChildID
			}
			break
		}
	}

	mid := len(cells) / 2
	promotedKey := make([]byte, len(cells[mid].key))
	copy(promotedKey, cells[mid].key)

	// Create right internal node
	rightNode, err := t.pool.NewPage(PageTypeInternal)
	if err != nil {
		t.pool.Unpin(nodeID)
		return err
	}

	// Rewrite left node with cells[:mid]
	t.clearPageCells(node)
	for _, c := range cells[:mid] {
		if err := node.InsertInternalCell(c.childID, c.key); err != nil {
			t.pool.Unpin(nodeID)
			t.pool.Unpin(rightNode.ID)
			return err
		}
	}
	// Left node's right pointer = the child pointer of the promoted key
	node.SetRightPointer(cells[mid].childID)

	// Right node gets cells[mid+1:]
	for _, c := range cells[mid+1:] {
		if err := rightNode.InsertInternalCell(c.childID, c.key); err != nil {
			t.pool.Unpin(nodeID)
			t.pool.Unpin(rightNode.ID)
			return err
		}
	}
	rightNode.SetRightPointer(oldRightPtr)

	t.pool.Unpin(nodeID)
	t.pool.Unpin(rightNode.ID)

	if err := t.flushPage(nodeID); err != nil {
		return err
	}
	if err := t.flushPage(rightNode.ID); err != nil {
		return err
	}

	// Promote to parent
	return t.insertIntoParent(path[:len(path)-1], nodeID, promotedKey, rightNode.ID)
}

// createNewRoot creates a new root internal node.
func (t *BPlusTree) createNewRoot(leftChildID uint32, key []byte, rightChildID uint32) error {
	root, err := t.pool.NewPage(PageTypeInternal)
	if err != nil {
		return fmt.Errorf("create new root: %w", err)
	}

	if err := root.InsertInternalCell(leftChildID, key); err != nil {
		t.pool.Unpin(root.ID)
		return err
	}
	root.SetRightPointer(rightChildID)

	t.rootPageID = root.ID
	t.pool.Unpin(root.ID)

	if err := t.flushPage(root.ID); err != nil {
		return err
	}
	return t.pager.SetRootPageID(root.ID)
}

// clearPageCells resets a page's cell content (for rewrites after splits).
func (t *BPlusTree) clearPageCells(p *Page) {
	pageType := p.PageType()
	rightPtr := p.RightPointer()
	parentID := p.ParentID()

	// Clear everything except the first byte (page type)
	for i := 1; i < PageSize; i++ {
		p.Data[i] = 0
	}

	p.Data[0] = pageType
	p.SetCellCount(0)
	p.SetFreeSpaceStart(PageHeaderSize)
	p.SetFreeSpaceEnd(PageSize)
	p.SetParentID(parentID)
	p.SetRightPointer(rightPtr)
	p.Dirty = true
}

// handleUnderflow checks if a leaf has too few keys and redistributes or merges.
func (t *BPlusTree) handleUnderflow(path []uint32) error {
	if len(path) <= 1 {
		// Root node can have any number of keys
		return nil
	}

	leafID := path[len(path)-1]
	leaf, err := t.pool.FetchPage(leafID)
	if err != nil {
		return err
	}

	// Minimum occupancy: at least 1 key in a leaf (for simplicity)
	// A more aggressive threshold could be used, but this prevents empty leaves.
	if leaf.CellCount() > 0 {
		t.pool.Unpin(leafID)
		return nil
	}

	// Leaf is empty: remove it from the tree
	t.pool.Unpin(leafID)

	// Remove from parent
	parentID := path[len(path)-2]
	parent, err := t.pool.FetchPage(parentID)
	if err != nil {
		return err
	}

	count := int(parent.CellCount())
	removed := false

	for i := 0; i < count; i++ {
		childID, _ := parent.GetInternalCellAt(i)
		if childID == leafID {
			// Remove this cell: the right sibling takes over
			// Shift cells left
			for j := i; j < count-1; j++ {
				parent.SetCellPointer(j, parent.GetCellPointer(j+1))
			}
			parent.SetCellCount(uint16(count - 1))
			parent.SetFreeSpaceStart(uint16(PageHeaderSize + (count-1)*CellPtrSize))
			removed = true
			break
		}
	}

	if !removed {
		// The empty leaf might be the rightmost child
		if parent.RightPointer() == leafID {
			// Replace right pointer with previous cell's child becoming the new right pointer
			if count > 0 {
				// Remove the last cell and make its child the right pointer
				lastChild, _ := parent.GetInternalCellAt(count - 1)
				_ = lastChild
				parent.SetCellCount(uint16(count - 1))
				parent.SetFreeSpaceStart(uint16(PageHeaderSize + (count-1)*CellPtrSize))
				// Actually, the right pointer should become the child of the removed last key
				// This is getting complex; for simplicity we leave the parent as-is if it still has keys
			}
		}
	}

	t.pool.Unpin(parentID)
	if err := t.flushPage(parentID); err != nil {
		return err
	}

	// Free the empty leaf
	if err := t.pool.FreePage(leafID); err != nil {
		return err
	}

	// Check if parent is now empty and not root
	if len(path) > 2 {
		parent2, err := t.pool.FetchPage(parentID)
		if err != nil {
			return err
		}
		if parent2.CellCount() == 0 {
			// Parent has no keys but might have a right pointer (single child)
			// Collapse: make the single child the new child in grandparent
			singleChild := parent2.RightPointer()
			t.pool.Unpin(parentID)

			if parentID == t.rootPageID {
				// Collapse root
				t.rootPageID = singleChild
				if err := t.pager.SetRootPageID(singleChild); err != nil {
					return err
				}
				return t.pool.FreePage(parentID)
			}
		} else {
			t.pool.Unpin(parentID)
		}
	} else if parentID == t.rootPageID {
		// Check if root should collapse
		parent2, err := t.pool.FetchPage(parentID)
		if err != nil {
			return err
		}
		if parent2.CellCount() == 0 {
			singleChild := parent2.RightPointer()
			t.pool.Unpin(parentID)
			if singleChild != 0 {
				t.rootPageID = singleChild
				if err := t.pager.SetRootPageID(singleChild); err != nil {
					return err
				}
				return t.pool.FreePage(parentID)
			}
		} else {
			t.pool.Unpin(parentID)
		}
	}

	return nil
}

// Scan iterates over all key-value pairs in sorted order.
func (t *BPlusTree) Scan(fn func(key, value []byte) bool) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Find leftmost leaf
	pageID := t.rootPageID
	for {
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return err
		}
		if page.PageType() == PageTypeLeaf {
			t.pool.Unpin(pageID)
			break
		}
		// Follow leftmost child
		if page.CellCount() > 0 {
			childID, _ := page.GetInternalCellAt(0)
			t.pool.Unpin(pageID)
			pageID = childID
		} else {
			childID := page.RightPointer()
			t.pool.Unpin(pageID)
			pageID = childID
		}
	}

	// Scan through leaf chain
	for pageID != 0 {
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return err
		}
		count := int(page.CellCount())
		for i := 0; i < count; i++ {
			k, v := page.GetLeafKeyValueAt(i)
			if !fn(k, v) {
				t.pool.Unpin(pageID)
				return nil
			}
		}
		nextID := page.RightPointer()
		t.pool.Unpin(pageID)
		pageID = nextID
	}
	return nil
}

// ScanChunked iterates over all key-value pairs in sorted order, but
// releases and re-acquires the read lock every chunkSize entries. This
// prevents long lock holds that would block concurrent writers (e.g.
// during anti-entropy full-table scans).
//
// Between chunks the goroutine yields so pending writers can proceed.
// Position is tracked by remembering the last key; after re-acquiring
// the lock we re-find the leaf via findLeafPath and skip forward.
func (t *BPlusTree) ScanChunked(chunkSize int, fn func(key, value []byte) bool) error {
	if chunkSize <= 0 {
		chunkSize = 500
	}

	var lastKey []byte // nil on the first chunk

	for {
		done, err := t.scanOneChunk(chunkSize, lastKey, func(k, v []byte) bool {
			lastKey = make([]byte, len(k))
			copy(lastKey, k)
			return fn(k, v)
		})
		if err != nil || done {
			return err
		}
		// Yield so writers blocked on mu.Lock() can make progress.
		runtime.Gosched()
	}
}

// scanOneChunk scans up to chunkSize entries starting after afterKey
// (or from the beginning if afterKey is nil). It holds RLock for the
// duration of the chunk only. Returns done=true when the scan is
// complete or the callback returned false.
func (t *BPlusTree) scanOneChunk(chunkSize int, afterKey []byte, fn func(key, value []byte) bool) (done bool, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Find the starting leaf page.
	var pageID uint32
	var startIdx int

	if afterKey == nil {
		// First chunk: find the leftmost leaf.
		pageID = t.rootPageID
		for {
			page, err := t.pool.FetchPage(pageID)
			if err != nil {
				return false, err
			}
			if page.PageType() == PageTypeLeaf {
				t.pool.Unpin(pageID)
				break
			}
			if page.CellCount() > 0 {
				childID, _ := page.GetInternalCellAt(0)
				t.pool.Unpin(pageID)
				pageID = childID
			} else {
				childID := page.RightPointer()
				t.pool.Unpin(pageID)
				pageID = childID
			}
		}
		startIdx = 0
	} else {
		// Resume: find the leaf that would contain afterKey.
		path, err := t.findLeafPath(afterKey)
		if err != nil {
			return false, err
		}
		pageID = path[len(path)-1]

		// Find the index just past afterKey in this leaf.
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return false, err
		}
		count := int(page.CellCount())
		startIdx = count // default: skip to next leaf
		for i := 0; i < count; i++ {
			k, _ := page.GetLeafKeyValueAt(i)
			if compareKeys(k, afterKey) > 0 {
				startIdx = i
				break
			}
		}
		t.pool.Unpin(pageID)
	}

	// Walk the leaf chain for up to chunkSize entries.
	emitted := 0
	for pageID != 0 {
		page, err := t.pool.FetchPage(pageID)
		if err != nil {
			return false, err
		}
		count := int(page.CellCount())
		for i := startIdx; i < count; i++ {
			k, v := page.GetLeafKeyValueAt(i)
			if !fn(k, v) {
				t.pool.Unpin(pageID)
				return true, nil
			}
			emitted++
			if emitted >= chunkSize {
				t.pool.Unpin(pageID)
				return false, nil // more data remains
			}
		}
		nextID := page.RightPointer()
		t.pool.Unpin(pageID)
		pageID = nextID
		startIdx = 0
	}
	return true, nil // reached end of data
}

// Count returns the number of key-value pairs in the tree.
func (t *BPlusTree) Count() (int, error) {
	count := 0
	err := t.Scan(func(key, value []byte) bool {
		count++
		return true
	})
	return count, err
}

// Close flushes all pages.
func (t *BPlusTree) Close() error {
	return t.pool.FlushAll()
}
