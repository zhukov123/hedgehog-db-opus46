package storage

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	PageSize = 4096

	// Page types
	PageTypeInternal  uint8 = 0x01
	PageTypeLeaf      uint8 = 0x02
	PageTypeFreeList  uint8 = 0x03
	PageTypeOverflow  uint8 = 0x04

	// File header constants
	FileHeaderSize = 128
	MagicString    = "HEDGEHOG"
	FileVersion    = uint32(1)

	// Page header: type(1) + flags(1) + cellCount(2) + freeSpaceStart(2) + freeSpaceEnd(2) + parentID(4) + rightSibling(4) = 16 bytes
	PageHeaderSize = 16

	// Cell pointer size (2 bytes each, offset into page)
	CellPtrSize = 2

	// Leaf cell: keyLen(2) + key + valLen(4) + value
	// Internal cell: childPageID(4) + keyLen(2) + key

	MaxKeySize   = 512
	MaxValueSize = 2048 // values larger than this use overflow pages

	// Overflow page header: type(1) + flags(1) + dataLen(2) + nextOverflow(4) = 8
	OverflowHeaderSize = 8
	OverflowDataSize   = PageSize - OverflowHeaderSize
)

var (
	ErrPageFull      = errors.New("page is full")
	ErrKeyNotFound   = errors.New("key not found")
	ErrKeyTooLarge   = errors.New("key exceeds maximum size")
	ErrValueTooLarge = errors.New("value exceeds maximum inline size")
	ErrInvalidPage   = errors.New("invalid page data")
	ErrCorruptPage   = errors.New("corrupt page data")
)

// FileHeader represents the database file header stored on page 0.
type FileHeader struct {
	Magic       [8]byte
	Version     uint32
	RootPageID  uint32
	TotalPages  uint32
	FreeListHead uint32
	WALLSN      uint64
	Checksum    uint32
	// Reserved for future use up to FileHeaderSize
}

func NewFileHeader() *FileHeader {
	h := &FileHeader{
		Version:     FileVersion,
		RootPageID:  0,
		TotalPages:  1, // page 0 is the header page
		FreeListHead: 0,
		WALLSN:      0,
	}
	copy(h.Magic[:], MagicString)
	return h
}

func (h *FileHeader) Serialize() []byte {
	buf := make([]byte, PageSize)
	copy(buf[0:8], h.Magic[:])
	binary.LittleEndian.PutUint32(buf[8:12], h.Version)
	binary.LittleEndian.PutUint32(buf[12:16], h.RootPageID)
	binary.LittleEndian.PutUint32(buf[16:20], h.TotalPages)
	binary.LittleEndian.PutUint32(buf[20:24], h.FreeListHead)
	binary.LittleEndian.PutUint64(buf[24:32], h.WALLSN)
	// Calculate checksum over everything except the checksum field itself (bytes 32-35)
	csum := crc32.ChecksumIEEE(buf[0:32])
	binary.LittleEndian.PutUint32(buf[32:36], csum)
	h.Checksum = csum
	return buf
}

func DeserializeFileHeader(data []byte) (*FileHeader, error) {
	if len(data) < 36 {
		return nil, ErrInvalidPage
	}
	h := &FileHeader{}
	copy(h.Magic[:], data[0:8])
	if string(h.Magic[:]) != MagicString {
		return nil, ErrInvalidPage
	}
	h.Version = binary.LittleEndian.Uint32(data[8:12])
	h.RootPageID = binary.LittleEndian.Uint32(data[12:16])
	h.TotalPages = binary.LittleEndian.Uint32(data[16:20])
	h.FreeListHead = binary.LittleEndian.Uint32(data[20:24])
	h.WALLSN = binary.LittleEndian.Uint64(data[24:32])
	h.Checksum = binary.LittleEndian.Uint32(data[32:36])

	expected := crc32.ChecksumIEEE(data[0:32])
	if h.Checksum != expected {
		return nil, ErrCorruptPage
	}
	return h, nil
}

// Page represents an in-memory page with raw bytes.
type Page struct {
	ID    uint32
	Data  []byte
	Dirty bool
}

func NewPage(id uint32, pageType uint8) *Page {
	p := &Page{
		ID:   id,
		Data: make([]byte, PageSize),
	}
	p.Data[0] = pageType
	// Initialize freeSpaceStart = PageHeaderSize (first cell pointer position)
	binary.LittleEndian.PutUint16(p.Data[4:6], PageHeaderSize)
	// Initialize freeSpaceEnd = PageSize (cells grow from the end)
	binary.LittleEndian.PutUint16(p.Data[6:8], PageSize)
	return p
}

// Page header accessors
func (p *Page) PageType() uint8 {
	return p.Data[0]
}

func (p *Page) SetPageType(t uint8) {
	p.Data[0] = t
	p.Dirty = true
}

func (p *Page) Flags() uint8 {
	return p.Data[1]
}

func (p *Page) SetFlags(f uint8) {
	p.Data[1] = f
	p.Dirty = true
}

func (p *Page) CellCount() uint16 {
	return binary.LittleEndian.Uint16(p.Data[2:4])
}

func (p *Page) SetCellCount(n uint16) {
	binary.LittleEndian.PutUint16(p.Data[2:4], n)
	p.Dirty = true
}

// FreeSpaceStart: offset where the next cell pointer will be written (grows right/down)
func (p *Page) FreeSpaceStart() uint16 {
	return binary.LittleEndian.Uint16(p.Data[4:6])
}

func (p *Page) SetFreeSpaceStart(offset uint16) {
	binary.LittleEndian.PutUint16(p.Data[4:6], offset)
	p.Dirty = true
}

// FreeSpaceEnd: offset where the next cell content will be written (grows left/up)
func (p *Page) FreeSpaceEnd() uint16 {
	return binary.LittleEndian.Uint16(p.Data[6:8])
}

func (p *Page) SetFreeSpaceEnd(offset uint16) {
	binary.LittleEndian.PutUint16(p.Data[6:8], offset)
	p.Dirty = true
}

func (p *Page) ParentID() uint32 {
	return binary.LittleEndian.Uint32(p.Data[8:12])
}

func (p *Page) SetParentID(id uint32) {
	binary.LittleEndian.PutUint32(p.Data[8:12], id)
	p.Dirty = true
}

// RightPointer: for internal nodes, the rightmost child; for leaf nodes, the next leaf sibling
func (p *Page) RightPointer() uint32 {
	return binary.LittleEndian.Uint32(p.Data[12:16])
}

func (p *Page) SetRightPointer(id uint32) {
	binary.LittleEndian.PutUint32(p.Data[12:16], id)
	p.Dirty = true
}

// FreeSpace returns the available bytes for new cells and pointers.
func (p *Page) FreeSpace() int {
	start := int(p.FreeSpaceStart())
	end := int(p.FreeSpaceEnd())
	return end - start
}

// GetCellPointer returns the offset stored at the i-th cell pointer slot.
func (p *Page) GetCellPointer(i int) uint16 {
	offset := PageHeaderSize + i*CellPtrSize
	return binary.LittleEndian.Uint16(p.Data[offset : offset+CellPtrSize])
}

// SetCellPointer writes an offset to the i-th cell pointer slot.
func (p *Page) SetCellPointer(i int, offset uint16) {
	pos := PageHeaderSize + i*CellPtrSize
	binary.LittleEndian.PutUint16(p.Data[pos:pos+CellPtrSize], offset)
	p.Dirty = true
}

// --- Leaf Cell Operations ---

// LeafCellSize returns the serialized size of a leaf cell.
func LeafCellSize(key, value []byte) int {
	// keyLen(2) + key + valLen(4) + value
	return 2 + len(key) + 4 + len(value)
}

// WriteLeafCell writes a leaf cell at the given offset and returns the number of bytes written.
func (p *Page) WriteLeafCell(offset int, key, value []byte) int {
	pos := offset
	binary.LittleEndian.PutUint16(p.Data[pos:pos+2], uint16(len(key)))
	pos += 2
	copy(p.Data[pos:pos+len(key)], key)
	pos += len(key)
	binary.LittleEndian.PutUint32(p.Data[pos:pos+4], uint32(len(value)))
	pos += 4
	copy(p.Data[pos:pos+len(value)], value)
	pos += len(value)
	p.Dirty = true
	return pos - offset
}

// ReadLeafCell reads a leaf cell at the given offset, returning key and value.
func (p *Page) ReadLeafCell(offset int) (key, value []byte) {
	pos := offset
	keyLen := int(binary.LittleEndian.Uint16(p.Data[pos : pos+2]))
	pos += 2
	key = make([]byte, keyLen)
	copy(key, p.Data[pos:pos+keyLen])
	pos += keyLen
	valLen := int(binary.LittleEndian.Uint32(p.Data[pos : pos+4]))
	pos += 4
	value = make([]byte, valLen)
	copy(value, p.Data[pos:pos+valLen])
	return key, value
}

// InsertLeafCell inserts a key-value pair into a leaf page in sorted order.
func (p *Page) InsertLeafCell(key, value []byte) error {
	cellSize := LeafCellSize(key, value)
	needed := cellSize + CellPtrSize
	if p.FreeSpace() < needed {
		return ErrPageFull
	}

	count := int(p.CellCount())

	// Binary search for insertion point
	insertIdx := p.leafSearchIndex(key)

	// Write cell content at end of free space (growing upward)
	newEnd := int(p.FreeSpaceEnd()) - cellSize
	p.WriteLeafCell(newEnd, key, value)
	p.SetFreeSpaceEnd(uint16(newEnd))

	// Shift cell pointers to make room
	for i := count; i > insertIdx; i-- {
		p.SetCellPointer(i, p.GetCellPointer(i-1))
	}

	// Write new cell pointer
	p.SetCellPointer(insertIdx, uint16(newEnd))
	p.SetCellCount(uint16(count + 1))

	// Update freeSpaceStart
	p.SetFreeSpaceStart(uint16(PageHeaderSize + (count+1)*CellPtrSize))

	return nil
}

// leafSearchIndex returns the index where a key should be inserted (binary search).
func (p *Page) leafSearchIndex(key []byte) int {
	count := int(p.CellCount())
	lo, hi := 0, count
	for lo < hi {
		mid := (lo + hi) / 2
		cellOffset := int(p.GetCellPointer(mid))
		existingKey, _ := p.ReadLeafCell(cellOffset)
		if compareKeys(existingKey, key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// SearchLeaf searches for a key in a leaf page and returns (value, found).
func (p *Page) SearchLeaf(key []byte) ([]byte, bool) {
	idx := p.leafSearchIndex(key)
	if idx < int(p.CellCount()) {
		cellOffset := int(p.GetCellPointer(idx))
		k, v := p.ReadLeafCell(cellOffset)
		if compareKeys(k, key) == 0 {
			return v, true
		}
	}
	return nil, false
}

// DeleteLeafCell removes a key from a leaf page. Returns true if found and deleted.
func (p *Page) DeleteLeafCell(key []byte) bool {
	idx := p.leafSearchIndex(key)
	count := int(p.CellCount())
	if idx >= count {
		return false
	}
	cellOffset := int(p.GetCellPointer(idx))
	k, _ := p.ReadLeafCell(cellOffset)
	if compareKeys(k, key) != 0 {
		return false
	}

	// Shift cell pointers left
	for i := idx; i < count-1; i++ {
		p.SetCellPointer(i, p.GetCellPointer(i+1))
	}
	p.SetCellCount(uint16(count - 1))
	p.SetFreeSpaceStart(uint16(PageHeaderSize + (count-1)*CellPtrSize))
	p.Dirty = true
	// Note: We don't reclaim the cell content space here (would need page compaction).
	// This is acceptable; compaction can be done during page splits or explicitly.
	return true
}

// GetLeafKeyAt returns the key at the given cell index in a leaf page.
func (p *Page) GetLeafKeyAt(idx int) []byte {
	cellOffset := int(p.GetCellPointer(idx))
	k, _ := p.ReadLeafCell(cellOffset)
	return k
}

// GetLeafKeyValueAt returns the key and value at the given cell index in a leaf page.
func (p *Page) GetLeafKeyValueAt(idx int) ([]byte, []byte) {
	cellOffset := int(p.GetCellPointer(idx))
	return p.ReadLeafCell(cellOffset)
}

// --- Internal Cell Operations ---

// InternalCellSize returns the serialized size of an internal cell.
func InternalCellSize(key []byte) int {
	// childPageID(4) + keyLen(2) + key
	return 4 + 2 + len(key)
}

// WriteInternalCell writes an internal cell at the given offset.
func (p *Page) WriteInternalCell(offset int, childPageID uint32, key []byte) int {
	pos := offset
	binary.LittleEndian.PutUint32(p.Data[pos:pos+4], childPageID)
	pos += 4
	binary.LittleEndian.PutUint16(p.Data[pos:pos+2], uint16(len(key)))
	pos += 2
	copy(p.Data[pos:pos+len(key)], key)
	pos += len(key)
	p.Dirty = true
	return pos - offset
}

// ReadInternalCell reads an internal cell at the given offset.
func (p *Page) ReadInternalCell(offset int) (childPageID uint32, key []byte) {
	pos := offset
	childPageID = binary.LittleEndian.Uint32(p.Data[pos : pos+4])
	pos += 4
	keyLen := int(binary.LittleEndian.Uint16(p.Data[pos : pos+2]))
	pos += 2
	key = make([]byte, keyLen)
	copy(key, p.Data[pos:pos+keyLen])
	return childPageID, key
}

// InsertInternalCell inserts a key and left child into an internal page in sorted order.
func (p *Page) InsertInternalCell(childPageID uint32, key []byte) error {
	cellSize := InternalCellSize(key)
	needed := cellSize + CellPtrSize
	if p.FreeSpace() < needed {
		return ErrPageFull
	}

	count := int(p.CellCount())

	// Binary search for insertion point
	insertIdx := p.internalSearchIndex(key)

	// Write cell content
	newEnd := int(p.FreeSpaceEnd()) - cellSize
	p.WriteInternalCell(newEnd, childPageID, key)
	p.SetFreeSpaceEnd(uint16(newEnd))

	// Shift cell pointers
	for i := count; i > insertIdx; i-- {
		p.SetCellPointer(i, p.GetCellPointer(i-1))
	}

	p.SetCellPointer(insertIdx, uint16(newEnd))
	p.SetCellCount(uint16(count + 1))
	p.SetFreeSpaceStart(uint16(PageHeaderSize + (count+1)*CellPtrSize))

	return nil
}

// internalSearchIndex returns the index where a key should be inserted in an internal page.
func (p *Page) internalSearchIndex(key []byte) int {
	count := int(p.CellCount())
	lo, hi := 0, count
	for lo < hi {
		mid := (lo + hi) / 2
		cellOffset := int(p.GetCellPointer(mid))
		_, existingKey := p.ReadInternalCell(cellOffset)
		if compareKeys(existingKey, key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// FindChild returns the child page ID to follow for the given search key in an internal page.
func (p *Page) FindChild(key []byte) uint32 {
	count := int(p.CellCount())
	for i := 0; i < count; i++ {
		cellOffset := int(p.GetCellPointer(i))
		childID, cellKey := p.ReadInternalCell(cellOffset)
		if compareKeys(key, cellKey) < 0 {
			return childID
		}
	}
	// Key is >= all keys, follow rightmost pointer
	return p.RightPointer()
}

// GetInternalKeyAt returns the key at the given cell index in an internal page.
func (p *Page) GetInternalKeyAt(idx int) []byte {
	cellOffset := int(p.GetCellPointer(idx))
	_, k := p.ReadInternalCell(cellOffset)
	return k
}

// GetInternalCellAt returns the child page ID and key at the given cell index.
func (p *Page) GetInternalCellAt(idx int) (uint32, []byte) {
	cellOffset := int(p.GetCellPointer(idx))
	return p.ReadInternalCell(cellOffset)
}

// --- Overflow Page Operations ---

// WriteOverflowPage writes data to an overflow page with a pointer to the next overflow page.
func WriteOverflowPage(data []byte, nextOverflow uint32) []byte {
	buf := make([]byte, PageSize)
	buf[0] = PageTypeOverflow
	buf[1] = 0 // flags
	dataLen := len(data)
	if dataLen > OverflowDataSize {
		dataLen = OverflowDataSize
	}
	binary.LittleEndian.PutUint16(buf[2:4], uint16(dataLen))
	binary.LittleEndian.PutUint32(buf[4:8], nextOverflow)
	copy(buf[OverflowHeaderSize:OverflowHeaderSize+dataLen], data[:dataLen])
	return buf
}

// ReadOverflowPage reads the data and next pointer from an overflow page.
func ReadOverflowPage(data []byte) (payload []byte, nextOverflow uint32) {
	dataLen := int(binary.LittleEndian.Uint16(data[2:4]))
	nextOverflow = binary.LittleEndian.Uint32(data[4:8])
	payload = make([]byte, dataLen)
	copy(payload, data[OverflowHeaderSize:OverflowHeaderSize+dataLen])
	return payload, nextOverflow
}

// compareKeys does lexicographic comparison of two byte slices.
func compareKeys(a, b []byte) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
