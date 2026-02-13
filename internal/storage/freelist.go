package storage

import (
	"encoding/binary"
	"sync"
)

const (
	// Each freelist page stores up to this many page IDs
	// Page header: type(1) + count(2) + nextFreeListPage(4) = 7 bytes
	FreeListHeaderSize  = 7
	FreeListEntrySize   = 4 // uint32 page ID
	MaxFreeListEntries  = (PageSize - FreeListHeaderSize) / FreeListEntrySize
)

// FreeList manages freed pages for reuse.
type FreeList struct {
	pager *Pager
	mu    sync.Mutex
}

// NewFreeList creates a new FreeList manager.
func NewFreeList(pager *Pager) *FreeList {
	return &FreeList{pager: pager}
}

// AllocatePage returns a free page or allocates a new one.
func (fl *FreeList) AllocatePage() (uint32, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	headID := fl.pager.Header().FreeListHead
	if headID == 0 {
		// No free pages, allocate new
		return fl.pager.AllocatePage()
	}

	// Read the freelist page
	page, err := fl.pager.ReadPage(headID)
	if err != nil {
		return 0, err
	}

	count := int(binary.LittleEndian.Uint16(page.Data[1:3]))
	if count == 0 {
		// Empty freelist page; reclaim this page itself and move to next
		nextPage := binary.LittleEndian.Uint32(page.Data[3:7])
		if err := fl.pager.SetFreeListHead(nextPage); err != nil {
			return 0, err
		}
		return headID, nil
	}

	// Pop the last entry
	count--
	entryOffset := FreeListHeaderSize + count*FreeListEntrySize
	freedPageID := binary.LittleEndian.Uint32(page.Data[entryOffset : entryOffset+4])

	// Update count
	binary.LittleEndian.PutUint16(page.Data[1:3], uint16(count))
	page.Dirty = true

	if err := fl.pager.WritePage(page); err != nil {
		return 0, err
	}

	return freedPageID, nil
}

// FreePage adds a page to the free list.
func (fl *FreeList) FreePage(pageID uint32) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	headID := fl.pager.Header().FreeListHead

	if headID != 0 {
		// Try to add to existing freelist page
		page, err := fl.pager.ReadPage(headID)
		if err != nil {
			return err
		}

		count := int(binary.LittleEndian.Uint16(page.Data[1:3]))
		if count < MaxFreeListEntries {
			// Room to add
			entryOffset := FreeListHeaderSize + count*FreeListEntrySize
			binary.LittleEndian.PutUint32(page.Data[entryOffset:entryOffset+4], pageID)
			binary.LittleEndian.PutUint16(page.Data[1:3], uint16(count+1))
			page.Dirty = true
			return fl.pager.WritePage(page)
		}
	}

	// Need a new freelist page. Use the freed page itself if we have no room.
	// Actually, allocate a new freelist page from the pager.
	newPageID, err := fl.pager.AllocatePage()
	if err != nil {
		return err
	}

	newPage := NewPage(newPageID, PageTypeFreeList)
	newPage.Data[0] = PageTypeFreeList
	// Store the freed page as the first entry
	binary.LittleEndian.PutUint16(newPage.Data[1:3], 1)
	binary.LittleEndian.PutUint32(newPage.Data[3:7], headID) // next -> old head
	binary.LittleEndian.PutUint32(newPage.Data[FreeListHeaderSize:FreeListHeaderSize+4], pageID)

	if err := fl.pager.WritePage(newPage); err != nil {
		return err
	}

	return fl.pager.SetFreeListHead(newPageID)
}
