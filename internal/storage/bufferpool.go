package storage

import (
	"fmt"
	"sync"
)

const DefaultBufferPoolSize = 1024

// BufferPool provides an LRU page cache with pin counting.
type BufferPool struct {
	pager    *Pager
	freelist *FreeList
	capacity int

	mu    sync.Mutex
	pages map[uint32]*bufferEntry

	// LRU doubly-linked list
	head *bufferEntry // most recently used
	tail *bufferEntry // least recently used
}

type bufferEntry struct {
	page     *Page
	pinCount int
	prev     *bufferEntry
	next     *bufferEntry
}

// NewBufferPool creates a buffer pool over a pager.
func NewBufferPool(pager *Pager, freelist *FreeList, capacity int) *BufferPool {
	if capacity <= 0 {
		capacity = DefaultBufferPoolSize
	}
	return &BufferPool{
		pager:    pager,
		freelist: freelist,
		capacity: capacity,
		pages:    make(map[uint32]*bufferEntry),
	}
}

// FetchPage fetches a page, returning it pinned. Caller must call Unpin when done.
func (bp *BufferPool) FetchPage(pageID uint32) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Check cache
	if entry, ok := bp.pages[pageID]; ok {
		entry.pinCount++
		bp.moveToFront(entry)
		return entry.page, nil
	}

	// Evict if at capacity
	if len(bp.pages) >= bp.capacity {
		if err := bp.evict(); err != nil {
			return nil, fmt.Errorf("buffer pool evict: %w", err)
		}
	}

	// Read from disk
	page, err := bp.pager.ReadPage(pageID)
	if err != nil {
		return nil, err
	}

	entry := &bufferEntry{
		page:     page,
		pinCount: 1,
	}
	bp.pages[pageID] = entry
	bp.pushFront(entry)

	return page, nil
}

// NewPage allocates a new page via the freelist and returns it pinned.
func (bp *BufferPool) NewPage(pageType uint8) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Evict if at capacity
	if len(bp.pages) >= bp.capacity {
		if err := bp.evict(); err != nil {
			return nil, fmt.Errorf("buffer pool evict: %w", err)
		}
	}

	pageID, err := bp.freelist.AllocatePage()
	if err != nil {
		return nil, err
	}

	page := NewPage(pageID, pageType)

	entry := &bufferEntry{
		page:     page,
		pinCount: 1,
	}
	bp.pages[pageID] = entry
	bp.pushFront(entry)

	return page, nil
}

// Unpin decrements the pin count on a page.
func (bp *BufferPool) Unpin(pageID uint32) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if entry, ok := bp.pages[pageID]; ok {
		if entry.pinCount > 0 {
			entry.pinCount--
		}
	}
}

// MarkDirty marks a page as dirty.
func (bp *BufferPool) MarkDirty(pageID uint32) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if entry, ok := bp.pages[pageID]; ok {
		entry.page.Dirty = true
	}
}

// FlushPage writes a dirty page to disk.
func (bp *BufferPool) FlushPage(pageID uint32) error {
	bp.mu.Lock()
	entry, ok := bp.pages[pageID]
	if !ok {
		bp.mu.Unlock()
		return nil
	}
	page := entry.page
	bp.mu.Unlock()

	if page.Dirty {
		if err := bp.pager.WritePage(page); err != nil {
			return err
		}
	}
	return nil
}

// FlushAll writes all dirty pages to disk and syncs.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	dirtyPages := make([]*Page, 0)
	for _, entry := range bp.pages {
		if entry.page.Dirty {
			dirtyPages = append(dirtyPages, entry.page)
		}
	}
	bp.mu.Unlock()

	for _, page := range dirtyPages {
		if err := bp.pager.WritePage(page); err != nil {
			return err
		}
	}
	return bp.pager.Sync()
}

// FreePage frees a page and removes it from the cache.
func (bp *BufferPool) FreePage(pageID uint32) error {
	bp.mu.Lock()
	if entry, ok := bp.pages[pageID]; ok {
		bp.removeFromList(entry)
		delete(bp.pages, pageID)
	}
	bp.mu.Unlock()

	return bp.freelist.FreePage(pageID)
}

// evict removes the least recently used unpinned page. Must be called with mu held.
func (bp *BufferPool) evict() error {
	// Walk from tail (LRU) to find an unpinned page
	for entry := bp.tail; entry != nil; entry = entry.prev {
		if entry.pinCount == 0 {
			// Flush if dirty
			if entry.page.Dirty {
				if err := bp.pager.WritePage(entry.page); err != nil {
					return err
				}
			}
			bp.removeFromList(entry)
			delete(bp.pages, entry.page.ID)
			return nil
		}
	}
	return fmt.Errorf("all pages are pinned, cannot evict")
}

// LRU list operations

func (bp *BufferPool) pushFront(entry *bufferEntry) {
	entry.prev = nil
	entry.next = bp.head
	if bp.head != nil {
		bp.head.prev = entry
	}
	bp.head = entry
	if bp.tail == nil {
		bp.tail = entry
	}
}

func (bp *BufferPool) moveToFront(entry *bufferEntry) {
	if entry == bp.head {
		return
	}
	bp.removeFromList(entry)
	bp.pushFront(entry)
}

func (bp *BufferPool) removeFromList(entry *bufferEntry) {
	if entry.prev != nil {
		entry.prev.next = entry.next
	} else {
		bp.head = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	} else {
		bp.tail = entry.prev
	}
	entry.prev = nil
	entry.next = nil
}

// Pager returns the underlying pager.
func (bp *BufferPool) Pager() *Pager {
	return bp.pager
}
