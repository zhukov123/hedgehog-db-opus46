package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func setupBufferPool(t *testing.T, capacity int) (*BufferPool, *Pager, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hedgehog-bp-*")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "test.db")
	pager, err := OpenPager(dbPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	freelist := NewFreeList(pager)
	if capacity <= 0 {
		capacity = 16
	}
	pool := NewBufferPool(pager, freelist, capacity)
	cleanup := func() {
		pool.FlushAll()
		pager.Close()
		os.RemoveAll(dir)
	}
	return pool, pager, cleanup
}

func TestBufferPool_NewFetchUnpin(t *testing.T) {
	pool, _, cleanup := setupBufferPool(t, 16)
	defer cleanup()

	page, err := pool.NewPage(PageTypeLeaf)
	if err != nil {
		t.Fatal(err)
	}
	pageID := page.ID
	if pageID == 0 {
		t.Fatal("expected non-zero page ID")
	}
	pool.Unpin(pageID)

	fetched, err := pool.FetchPage(pageID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.ID != pageID {
		t.Errorf("FetchPage: got ID %d, want %d", fetched.ID, pageID)
	}
	pool.Unpin(pageID)
}

func TestBufferPool_MarkDirtyFlushPage(t *testing.T) {
	pool, _, cleanup := setupBufferPool(t, 16)
	defer cleanup()

	page, err := pool.NewPage(PageTypeLeaf)
	if err != nil {
		t.Fatal(err)
	}
	pageID := page.ID
	page.Data[10] = 0xab
	pool.MarkDirty(pageID)
	pool.Unpin(pageID)

	if err := pool.FlushPage(pageID); err != nil {
		t.Fatal(err)
	}

	pool.Unpin(pageID)
	// Re-fetch (may read from disk if evicted)
	fetched, err := pool.FetchPage(pageID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Data[10] != 0xab {
		t.Errorf("FlushPage: data not persisted, got %x", fetched.Data[10])
	}
	pool.Unpin(pageID)
}

func TestBufferPool_EvictionAtCapacity(t *testing.T) {
	capacity := 4
	pool, _, cleanup := setupBufferPool(t, capacity)
	defer cleanup()

	var ids []uint32
	for i := 0; i < capacity+2; i++ {
		page, err := pool.NewPage(PageTypeLeaf)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, page.ID)
		pool.Unpin(page.ID)
	}

	// All pages should be fetchable (some may have been evicted and re-read)
	for _, id := range ids {
		_, err := pool.FetchPage(id)
		if err != nil {
			t.Fatalf("FetchPage %d: %v", id, err)
		}
		pool.Unpin(id)
	}
}

func TestBufferPool_ConcurrentFetchPage(t *testing.T) {
	pool, _, cleanup := setupBufferPool(t, 64)
	defer cleanup()

	// Create a few pages
	var ids []uint32
	for i := 0; i < 10; i++ {
		page, err := pool.NewPage(PageTypeLeaf)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, page.ID)
		pool.Unpin(page.ID)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				p, err := pool.FetchPage(id)
				if err != nil {
					t.Error(err)
					return
				}
				_ = p
				pool.Unpin(id)
			}
		}()
	}
	wg.Wait()
}
