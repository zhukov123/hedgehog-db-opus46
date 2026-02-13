# Storage Engine Internals

## File Format

The database file is divided into fixed-size 4096-byte pages.

```
┌──────────────────────────────┐  offset 0
│     Page 0: File Header      │
├──────────────────────────────┤  offset 4096
│     Page 1: (data page)      │
├──────────────────────────────┤  offset 8192
│     Page 2: (data page)      │
├──────────────────────────────┤
│           ...                │
└──────────────────────────────┘
```

### File Header (Page 0)

Stored in the first 36 bytes of page 0:

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 8 | Magic | `"HEDGEHOG"` — identifies the file format |
| 8 | 4 | Version | File format version (currently 1) |
| 12 | 4 | RootPageID | Page ID of the B+ tree root |
| 16 | 4 | TotalPages | Total number of pages in the file |
| 20 | 4 | FreeListHead | Page ID of the first freelist page (0 = none) |
| 24 | 8 | WALLSN | Last WAL log sequence number |
| 32 | 4 | Checksum | CRC32 of bytes 0–31 |

Key code: `page.go:FileHeader`, `NewFileHeader()`, `Serialize()`, `DeserializeFileHeader()`

## Page Layout

### Common Page Header (16 bytes)

Every data page starts with this header:

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 1 | PageType | 0x01=Internal, 0x02=Leaf, 0x03=FreeList, 0x04=Overflow |
| 1 | 1 | Flags | Reserved |
| 2 | 2 | CellCount | Number of cells in this page |
| 4 | 2 | FreeSpaceStart | Offset where next cell pointer will go (grows right →) |
| 6 | 2 | FreeSpaceEnd | Offset where next cell content will go (grows left ←) |
| 8 | 4 | ParentID | Parent page ID (currently unused, reserved) |
| 12 | 4 | RightPointer | For internal: rightmost child. For leaf: next leaf sibling |

### Leaf Page Layout

```
┌──────────────────────────────────────────────────────────────┐
│ Header (16 bytes)                                            │
├──────────────────────────────────────────────────────────────┤
│ Cell Pointer Array (2 bytes each, sorted by key)             │
│ [ptr0][ptr1][ptr2]...                                        │
│                    ↓ FreeSpaceStart                           │
│         (free space)                                         │
│                    ↑ FreeSpaceEnd                             │
│ Cell Content Area (grows upward from bottom)                 │
│ ┌────────────────────────────────────────┐                   │
│ │ Cell: keyLen(2) | key | valLen(4) | value │                │
│ └────────────────────────────────────────┘                   │
└──────────────────────────────────────────────────────────────┘
```

- Cell pointers grow downward (from offset 16)
- Cell content grows upward (from offset 4095)
- Free space is between FreeSpaceStart and FreeSpaceEnd
- Cell pointers are **sorted by key** — enables binary search without reading all cells

### Leaf Cell Format

| Field | Size | Description |
|-------|------|-------------|
| keyLen | 2 bytes | Length of the key |
| key | variable | Raw key bytes (UTF-8) |
| valLen | 4 bytes | Length of the value |
| value | variable | Raw value bytes (JSON) |

Key code: `page.go:LeafCellSize()`, `WriteLeafCell()`, `ReadLeafCell()`, `InsertLeafCell()`

### Internal Page Layout

Same structure as leaf but with different cell format:

### Internal Cell Format

| Field | Size | Description |
|-------|------|-------------|
| childPageID | 4 bytes | Page ID of the left child |
| keyLen | 2 bytes | Length of the separator key |
| key | variable | Separator key bytes |

The **rightmost child** is stored in the page header's RightPointer field (offset 12).

Key code: `page.go:InternalCellSize()`, `WriteInternalCell()`, `ReadInternalCell()`, `InsertInternalCell()`

### How FindChild Works

```
Internal node: [child0|key0] [child1|key1] [child2|key2] rightPtr

To find the child for search key K:
  - If K < key0 → follow child0
  - If key0 ≤ K < key1 → follow child1
  - If key1 ≤ K < key2 → follow child2
  - If K ≥ key2 → follow rightPtr
```

Key code: `page.go:FindChild()`

## B+ Tree Operations

All operations are O(log n) where n = number of keys.

### Search

```
func (t *BPlusTree) Search(key) → value, error:
  1. Start at rootPageID
  2. Fetch page from BufferPool
  3. If leaf: binary search cell pointers → return value or ErrKeyNotFound
  4. If internal: FindChild(key) → get child page ID → goto step 2
```

Key code: `btree.go:Search()`

### Insert

```
func (t *BPlusTree) Insert(key, value) → error:
  1. findLeafPath(key) → returns path from root to target leaf [root, ..., leaf]
  2. If key exists in leaf: delete old cell, re-insert (update)
  3. Try InsertLeafCell(key, value)
  4. If page has space: done, flush page
  5. If ErrPageFull: call splitAndInsert()
```

### Leaf Split

```
func (t *BPlusTree) splitAndInsert(path, key, value):
  1. Collect ALL key-value pairs from the full leaf + the new pair
  2. Sort them (they're already sorted, just merge the new one in)
  3. Split in half: left gets pairs[:mid], right gets pairs[mid:]
  4. Create new right leaf page
  5. Rewrite left leaf with its half (clearPageCells then re-insert)
  6. Write right leaf with its half
  7. Link leaves: left.RightPointer = right.ID
  8. Promoted key = first key of the right leaf (pairs[mid].key)
  9. Call insertIntoParent(parentPath, leftID, promotedKey, rightID)
```

### Internal Split

```
func (t *BPlusTree) splitInternalNode(path, leftChild, key, rightChild):
  1. Collect all cells from the full internal node + new cell
  2. Sort them
  3. Middle key is promoted (NOT kept in either child)
  4. Left node gets cells[:mid]
  5. Right node gets cells[mid+1:]
  6. Left.RightPointer = cells[mid].childID
  7. Call insertIntoParent() recursively
```

### Root Split

```
func (t *BPlusTree) createNewRoot(leftChild, key, rightChild):
  1. Allocate new internal page
  2. Insert single cell: [leftChild | key]
  3. Set RightPointer = rightChild
  4. Update rootPageID in pager header
```

### Delete

```
func (t *BPlusTree) Delete(key) → error:
  1. findLeafPath(key)
  2. Fetch leaf, call DeleteLeafCell(key)
  3. If key not found: return ErrKeyNotFound
  4. Flush page
  5. Call handleUnderflow(path)
```

### Underflow Handling

```
func (t *BPlusTree) handleUnderflow(path):
  1. If root: do nothing (root can have any number of keys)
  2. If leaf has > 0 keys: do nothing (minimum occupancy = 1)
  3. If leaf is empty:
     a. Remove the corresponding cell from parent internal node
     b. Free the empty leaf page
     c. If parent is now empty and has only one child (RightPointer):
        - If parent is root: collapse root (root = single child)
        - Otherwise: leave it (simplified; full implementation would cascade)
```

**Known limitation**: The underflow handling is simplified. It handles empty leaf removal and root collapse but does not implement full sibling redistribution or internal node merging. This means after heavy deletions, the tree may be slightly less balanced than optimal. This is acceptable for the current use case; a compaction/rebuild operation could be added later.

### Scan

```
func (t *BPlusTree) Scan(fn) → error:
  1. Find leftmost leaf: from root, always follow the first child (or child0)
  2. Iterate through leaf chain via RightPointer
  3. For each leaf, iterate cells 0..CellCount-1 in order
  4. Call fn(key, value) for each — stop if fn returns false
```

## Buffer Pool

`bufferpool.go` — LRU page cache with pin counting.

### Key concepts

- **Pin count**: When a page is fetched, its pin count is incremented. The caller MUST call `Unpin(pageID)` when done. Pinned pages cannot be evicted.
- **LRU list**: Doubly-linked list. Most recently used at head, least recently used at tail.
- **Capacity**: Default 1024 pages (4 MB). When full, evicts the least recently used unpinned page.
- **Dirty tracking**: Pages that have been modified are marked dirty. Dirty pages are flushed to disk before eviction.

### Operations

| Method | Description |
|--------|-------------|
| `FetchPage(id)` | Return page from cache or read from disk. Pinned. |
| `NewPage(type)` | Allocate via freelist, create in-memory page. Pinned. |
| `Unpin(id)` | Decrement pin count. |
| `MarkDirty(id)` | Mark page as needing flush. |
| `FlushPage(id)` | Write dirty page to disk. |
| `FlushAll()` | Write all dirty pages + fsync. |
| `FreePage(id)` | Remove from cache + return to freelist. |

### Eviction

```
func evict():
  Walk from tail (LRU) toward head:
    If entry.pinCount == 0:
      If dirty: flush to disk
      Remove from LRU list and page map
      Return
  If all pages are pinned: return error
```

## Pager

`pager.go` — Raw disk I/O for pages.

| Method | Description |
|--------|-------------|
| `OpenPager(path)` | Open/create database file, read/write header |
| `ReadPage(id)` | Read 4096 bytes at offset `id * 4096` |
| `WritePage(page)` | Write 4096 bytes at offset `page.ID * 4096` |
| `AllocatePage()` | Increment TotalPages, extend file, return new ID |
| `SetRootPageID(id)` | Update header and flush |
| `Sync()` | fsync the file |

## Free List

`freelist.go` — Tracks freed pages for reuse.

### FreeList Page Format

| Offset | Size | Field |
|--------|------|-------|
| 0 | 1 | PageType (0x03) |
| 1 | 2 | Count of entries |
| 3 | 4 | Next FreeList page ID |
| 7 | 4×N | Page IDs of free pages |

Each freelist page holds up to `(4096 - 7) / 4 = 1022` page IDs.

### Operations

- `AllocatePage()`: Pop from freelist, or allocate new from pager if freelist is empty
- `FreePage(id)`: Push onto the head freelist page, or create a new freelist page if full

## WAL (Write-Ahead Log)

`wal.go` — Crash recovery via after-image logging.

### Record Format

| Field | Size | Description |
|-------|------|-------------|
| LSN | 8 | Log sequence number (monotonically increasing) |
| TxID | 8 | Transaction ID |
| Type | 1 | 0x01=PageWrite, 0x02=Commit, 0x03=Checkpoint |
| PageID | 4 | Target page (for PageWrite) |
| DataLen | 4 | Length of data payload |
| CRC | 4 | CRC32 of header + data |
| Data | variable | After-image of the page (for PageWrite) |

### Protocol

```
Write path:
  1. Log PageWrite record (after-image of the page) → fsync WAL
  2. Mark page dirty in buffer pool
  3. Lazy flush to data file (on eviction or explicit flush)
  4. Log Commit record → fsync WAL

Recovery:
  1. Read all WAL records
  2. Group PageWrite records by TxID
  3. Find Commit records → mark those TxIDs as committed
  4. On Checkpoint record → discard everything before it
  5. Apply committed page writes to the .db file
  6. Discard uncommitted page writes

Checkpoint:
  1. Flush all dirty pages to data file
  2. Fsync data file
  3. Log Checkpoint record
  4. Truncate WAL file
```

**Current integration note**: The WAL is opened and recovered when a table is opened (`table.go:OpenTable`). However, the B+ tree operations currently flush pages directly without going through the WAL write path. The WAL infrastructure is in place for full integration — to enable it, B+ tree writes would need to call `WAL.LogPageWrite()` before flushing and `WAL.LogCommit()` after the operation completes. This is the next step for full crash safety.

## Key Encoding

`encoding.go` — Keys are stored as raw UTF-8 bytes. Comparison is lexicographic byte-by-byte (`page.go:compareKeys()`). This means string keys sort in natural alphabetical order.

## Overflow Pages

For values exceeding `MaxValueSize` (2048 bytes), overflow pages chain the data:

| Offset | Size | Field |
|--------|------|-------|
| 0 | 1 | PageType (0x04) |
| 1 | 1 | Flags |
| 2 | 2 | DataLen (bytes in this page) |
| 4 | 4 | NextOverflow page ID (0 = last) |
| 8 | variable | Data payload (up to 4088 bytes) |

**Current status**: Overflow page read/write functions exist (`WriteOverflowPage`, `ReadOverflowPage`) but are not yet integrated into the B+ tree insert/search paths. Values must currently fit within a single leaf page cell.
