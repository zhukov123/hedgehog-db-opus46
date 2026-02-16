package storage

import (
	"fmt"
	"sync"
	"time"
)

const (
	DefaultBufferPoolSize = 1024
	DefaultShardCount     = 16

	// DefaultFlushInterval is the default interval for the background dirty-page flush loop.
	DefaultFlushInterval = 100 * time.Millisecond
)

// bufferShard holds a subset of cached pages, with its own lock and LRU list.
type bufferShard struct {
	mu    sync.Mutex
	pages map[uint32]*bufferEntry
	cap   int

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

// BufferPool provides a sharded LRU page cache with pin counting.
// Pages are assigned to shards by pageID % nShards, so concurrent accesses
// to different pages typically hit different locks.
type BufferPool struct {
	pager    *Pager
	freelist *FreeList
	shards   []*bufferShard
	nShards  uint32

	// Background flush loop
	flushInterval time.Duration
	stopFlush     chan struct{}
	flushDone     chan struct{}
}

// NewBufferPool creates a sharded buffer pool over a pager.
func NewBufferPool(pager *Pager, freelist *FreeList, capacity int) *BufferPool {
	if capacity <= 0 {
		capacity = DefaultBufferPoolSize
	}
	nShards := uint32(DefaultShardCount)
	if int(nShards) > capacity {
		nShards = uint32(capacity)
	}

	shards := make([]*bufferShard, nShards)
	perShard := capacity / int(nShards)
	remainder := capacity % int(nShards)
	for i := uint32(0); i < nShards; i++ {
		c := perShard
		if int(i) < remainder {
			c++
		}
		shards[i] = &bufferShard{
			pages: make(map[uint32]*bufferEntry),
			cap:   c,
		}
	}

	return &BufferPool{
		pager:         pager,
		freelist:      freelist,
		shards:        shards,
		nShards:       nShards,
		flushInterval: DefaultFlushInterval,
	}
}

func (bp *BufferPool) getShard(pageID uint32) *bufferShard {
	return bp.shards[pageID%bp.nShards]
}

// FetchPage fetches a page, returning it pinned. Caller must call Unpin when done.
func (bp *BufferPool) FetchPage(pageID uint32) (*Page, error) {
	s := bp.getShard(pageID)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check cache
	if entry, ok := s.pages[pageID]; ok {
		entry.pinCount++
		s.moveToFront(entry)
		return entry.page, nil
	}

	// Evict if at capacity
	if len(s.pages) >= s.cap {
		if err := s.evict(bp.pager); err != nil {
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
	s.pages[pageID] = entry
	s.pushFront(entry)

	return page, nil
}

// NewPage allocates a new page via the freelist and returns it pinned.
func (bp *BufferPool) NewPage(pageType uint8) (*Page, error) {
	pageID, err := bp.freelist.AllocatePage()
	if err != nil {
		return nil, err
	}

	s := bp.getShard(pageID)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict if at capacity
	if len(s.pages) >= s.cap {
		if err := s.evict(bp.pager); err != nil {
			return nil, fmt.Errorf("buffer pool evict: %w", err)
		}
	}

	page := NewPage(pageID, pageType)

	entry := &bufferEntry{
		page:     page,
		pinCount: 1,
	}
	s.pages[pageID] = entry
	s.pushFront(entry)

	return page, nil
}

// Unpin decrements the pin count on a page.
func (bp *BufferPool) Unpin(pageID uint32) {
	s := bp.getShard(pageID)
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.pages[pageID]; ok {
		if entry.pinCount > 0 {
			entry.pinCount--
		}
	}
}

// MarkDirty marks a page as dirty.
func (bp *BufferPool) MarkDirty(pageID uint32) {
	s := bp.getShard(pageID)
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.pages[pageID]; ok {
		entry.page.Dirty = true
	}
}

// FlushPage writes a dirty page to disk.
func (bp *BufferPool) FlushPage(pageID uint32) error {
	s := bp.getShard(pageID)
	s.mu.Lock()
	entry, ok := s.pages[pageID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	page := entry.page
	s.mu.Unlock()

	if page.Dirty {
		if err := bp.pager.WritePage(page); err != nil {
			return err
		}
	}
	return nil
}

// FlushAll writes all dirty pages to disk and syncs.
func (bp *BufferPool) FlushAll() error {
	dirtyPages := make([]*Page, 0)
	for _, s := range bp.shards {
		s.mu.Lock()
		for _, entry := range s.pages {
			if entry.page.Dirty {
				dirtyPages = append(dirtyPages, entry.page)
			}
		}
		s.mu.Unlock()
	}

	for _, page := range dirtyPages {
		if err := bp.pager.WritePage(page); err != nil {
			return err
		}
	}
	return bp.pager.Sync()
}

// GetCachedPageData returns a copy of the cached page data for WAL logging.
// Returns nil, false if the page is not in the cache.
func (bp *BufferPool) GetCachedPageData(pageID uint32) ([]byte, bool) {
	s := bp.getShard(pageID)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.pages[pageID]
	if !ok {
		return nil, false
	}
	data := make([]byte, len(entry.page.Data))
	copy(data, entry.page.Data)
	return data, true
}

// SetFlushInterval configures the background flush interval. Must be called before StartFlushLoop.
func (bp *BufferPool) SetFlushInterval(d time.Duration) {
	bp.flushInterval = d
}

// StartFlushLoop starts a background goroutine that periodically writes dirty pages to disk.
func (bp *BufferPool) StartFlushLoop() {
	bp.stopFlush = make(chan struct{})
	bp.flushDone = make(chan struct{})
	go bp.flushLoop()
}

// StopFlushLoop stops the background flush loop and waits for it to finish.
func (bp *BufferPool) StopFlushLoop() {
	if bp.stopFlush != nil {
		close(bp.stopFlush)
		<-bp.flushDone
		bp.stopFlush = nil
	}
}

func (bp *BufferPool) flushLoop() {
	defer close(bp.flushDone)
	ticker := time.NewTicker(bp.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-bp.stopFlush:
			return
		case <-ticker.C:
			bp.flushDirtyPages()
		}
	}
}

// flushDirtyPages writes all dirty unpinned pages to disk across all shards.
func (bp *BufferPool) flushDirtyPages() {
	for _, s := range bp.shards {
		s.mu.Lock()
		toFlush := make([]*Page, 0, 8)
		for _, entry := range s.pages {
			if entry.page.Dirty && entry.pinCount == 0 {
				toFlush = append(toFlush, entry.page)
			}
		}
		s.mu.Unlock()

		for _, page := range toFlush {
			_ = bp.pager.WritePage(page)
		}
	}
}

// FreePage frees a page and removes it from the cache.
func (bp *BufferPool) FreePage(pageID uint32) error {
	s := bp.getShard(pageID)
	s.mu.Lock()
	if entry, ok := s.pages[pageID]; ok {
		s.removeFromList(entry)
		delete(s.pages, pageID)
	}
	s.mu.Unlock()

	return bp.freelist.FreePage(pageID)
}

// Pager returns the underlying pager.
func (bp *BufferPool) Pager() *Pager {
	return bp.pager
}

// ---------------------------------------------------------------------------
// Per-shard LRU eviction and linked-list operations
// ---------------------------------------------------------------------------

// evict removes the least recently used unpinned page from the shard. Must be called with s.mu held.
func (s *bufferShard) evict(pager *Pager) error {
	for entry := s.tail; entry != nil; entry = entry.prev {
		if entry.pinCount == 0 {
			// Flush if dirty
			if entry.page.Dirty {
				if err := pager.WritePage(entry.page); err != nil {
					return err
				}
			}
			s.removeFromList(entry)
			delete(s.pages, entry.page.ID)
			return nil
		}
	}
	return fmt.Errorf("all pages in shard are pinned, cannot evict")
}

func (s *bufferShard) pushFront(entry *bufferEntry) {
	entry.prev = nil
	entry.next = s.head
	if s.head != nil {
		s.head.prev = entry
	}
	s.head = entry
	if s.tail == nil {
		s.tail = entry
	}
}

func (s *bufferShard) moveToFront(entry *bufferEntry) {
	if entry == s.head {
		return
	}
	s.removeFromList(entry)
	s.pushFront(entry)
}

func (s *bufferShard) removeFromList(entry *bufferEntry) {
	if entry.prev != nil {
		entry.prev.next = entry.next
	} else {
		s.head = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	} else {
		s.tail = entry.prev
	}
	entry.prev = nil
	entry.next = nil
}
