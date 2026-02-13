package storage

import (
	"fmt"
	"os"
	"sync"
)

// Pager manages raw disk I/O for database pages.
type Pager struct {
	file     *os.File
	filePath string
	header   *FileHeader
	mu       sync.RWMutex
}

// OpenPager opens or creates a database file.
func OpenPager(path string) (*Pager, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open pager: %w", err)
	}

	p := &Pager{
		file:     file,
		filePath: path,
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat file: %w", err)
	}

	if stat.Size() == 0 {
		// New database: write file header
		p.header = NewFileHeader()
		if err := p.writeHeaderPage(); err != nil {
			file.Close()
			return nil, err
		}
	} else {
		// Existing database: read and validate header
		if err := p.readHeaderPage(); err != nil {
			file.Close()
			return nil, err
		}
	}

	return p, nil
}

func (p *Pager) writeHeaderPage() error {
	data := p.header.Serialize()
	_, err := p.file.WriteAt(data, 0)
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	return nil
}

func (p *Pager) readHeaderPage() error {
	data := make([]byte, PageSize)
	_, err := p.file.ReadAt(data, 0)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	h, err := DeserializeFileHeader(data)
	if err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	p.header = h
	return nil
}

// ReadPage reads a page from disk by its ID.
func (p *Pager) ReadPage(pageID uint32) (*Page, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if pageID == 0 {
		return nil, fmt.Errorf("cannot read page 0 as a data page")
	}

	offset := int64(pageID) * PageSize
	data := make([]byte, PageSize)
	n, err := p.file.ReadAt(data, offset)
	if err != nil {
		return nil, fmt.Errorf("read page %d: %w", pageID, err)
	}
	if n != PageSize {
		return nil, fmt.Errorf("short read for page %d: got %d bytes", pageID, n)
	}

	page := &Page{
		ID:   pageID,
		Data: data,
	}
	return page, nil
}

// WritePage writes a page to disk.
func (p *Pager) WritePage(page *Page) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if page.ID == 0 {
		return fmt.Errorf("cannot write page 0 as a data page")
	}

	offset := int64(page.ID) * PageSize
	_, err := p.file.WriteAt(page.Data, offset)
	if err != nil {
		return fmt.Errorf("write page %d: %w", page.ID, err)
	}
	page.Dirty = false
	return nil
}

// AllocatePage allocates a new page and returns its ID.
func (p *Pager) AllocatePage() (uint32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pageID := p.header.TotalPages
	p.header.TotalPages++

	// Extend the file
	offset := int64(pageID) * PageSize
	zeros := make([]byte, PageSize)
	_, err := p.file.WriteAt(zeros, offset)
	if err != nil {
		return 0, fmt.Errorf("allocate page %d: %w", pageID, err)
	}

	// Persist header
	if err := p.writeHeaderPage(); err != nil {
		return 0, err
	}

	return pageID, nil
}

// Header returns the current file header.
func (p *Pager) Header() *FileHeader {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.header
}

// SetRootPageID updates the root page in the file header.
func (p *Pager) SetRootPageID(id uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.header.RootPageID = id
	return p.writeHeaderPage()
}

// SetFreeListHead updates the freelist head in the file header.
func (p *Pager) SetFreeListHead(id uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.header.FreeListHead = id
	return p.writeHeaderPage()
}

// UpdateWALLSN updates the WAL LSN in the file header.
func (p *Pager) UpdateWALLSN(lsn uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.header.WALLSN = lsn
	return p.writeHeaderPage()
}

// Sync flushes all pending writes to disk.
func (p *Pager) Sync() error {
	return p.file.Sync()
}

// Close closes the pager and the underlying file.
func (p *Pager) Close() error {
	if err := p.Sync(); err != nil {
		return err
	}
	return p.file.Close()
}

// TotalPages returns the total number of pages in the file.
func (p *Pager) TotalPages() uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.header.TotalPages
}
