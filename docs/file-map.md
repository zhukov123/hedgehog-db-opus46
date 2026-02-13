# File Map

Every file in the project with its purpose, key types, and key functions.

---

## Root

| File | Purpose |
|------|---------|
| `go.mod` | Go module definition: `github.com/hedgehog-db/hedgehog` |
| `webui.go` | `go:embed` directive embedding `web/dist/` as `WebDist embed.FS` |
| `Makefile` | Build, test, run, cluster, seed, bench, lint commands |

---

## cmd/hedgehogdb/main.go

**Server entry point.** Parses flags, initializes all components, wires them together, starts HTTP server with graceful shutdown.

Key flow: config → catalog → table manager → ring → membership → coordinator → replicator → anti-entropy → API server → register routes → start

Flag defaults: `-bind 0.0.0.0:8080 -data-dir ./data -node-id node-1`

---

## cmd/hedgehogctl/main.go

**CLI tool.** Stateless HTTP client that sends requests to the server.

Commands: `create-table`, `list-tables`, `delete-table`, `get`, `put`, `delete`, `scan`, `cluster-status`, `cluster-nodes`

Uses `HEDGEHOG_URL` env var (default `http://localhost:8080`).

---

## internal/storage/

### page.go

**Page layout, serialization, cell operations.**

Constants:
- `PageSize = 4096`
- `PageHeaderSize = 16`
- `PageTypeLeaf = 0x02`, `PageTypeInternal = 0x01`, `PageTypeFreeList = 0x03`, `PageTypeOverflow = 0x04`
- `MaxKeySize = 512`, `MaxValueSize = 2048`

Types:
- `FileHeader` — database file header (magic, version, root, total pages, freelist head, WAL LSN, CRC)
- `Page` — in-memory page with `ID uint32`, `Data []byte`, `Dirty bool`

Key functions on `Page`:
- `PageType()`, `CellCount()`, `FreeSpaceStart()`, `FreeSpaceEnd()`, `RightPointer()` — header accessors
- `InsertLeafCell(key, value)` → binary search insertion point, write cell at end, shift pointers
- `SearchLeaf(key)` → binary search, return (value, found)
- `DeleteLeafCell(key)` → find and remove, shift pointers
- `GetLeafKeyValueAt(idx)` — read cell at index
- `InsertInternalCell(childID, key)` — sorted insertion into internal node
- `FindChild(key)` — find which child pointer to follow for a search key
- `ReadLeafCell(offset)`, `ReadInternalCell(offset)` — raw cell deserialization
- `compareKeys(a, b)` — lexicographic byte comparison

### pager.go

**Disk I/O for pages.**

Type: `Pager` — holds `*os.File`, `*FileHeader`, `sync.RWMutex`

Functions:
- `OpenPager(path)` → create or open database file, read/write header
- `ReadPage(pageID)` → read 4096 bytes at `pageID * 4096`
- `WritePage(page)` → write page data to disk
- `AllocatePage()` → increment TotalPages, extend file, return new ID
- `SetRootPageID(id)` → update header
- `SetFreeListHead(id)` → update header
- `Sync()` → fsync
- `Close()` → sync + close file

### btree.go

**B+ tree with search, insert, delete, scan.**

Type: `BPlusTree` — holds `*BufferPool`, `*Pager`, `rootPageID uint32`, `sync.RWMutex`

Functions:
- `OpenBPlusTree(pool, pager, rootPageID)` → if rootPageID is 0, creates initial root leaf
- `Search(key)` → root-to-leaf traversal, returns value or `ErrKeyNotFound`
- `Insert(key, value)` → find leaf, insert or update, split if needed
- `Delete(key)` → find leaf, remove, handle underflow
- `Scan(fn)` → iterate all keys in sorted order via leaf chain
- `Count()` → count all keys via scan

Internal functions:
- `findLeafPath(key)` → returns `[]uint32` path from root to target leaf
- `splitAndInsert(path, key, value)` → leaf split: collect all KVs + new one, split at mid, promote key
- `splitInternalNode(path, leftChild, key, rightChild)` → internal node split
- `insertIntoParent(parentPath, leftChild, key, rightChild)` → recursive promotion
- `createNewRoot(leftChild, key, rightChild)` → new root when current root splits
- `clearPageCells(page)` → reset page content for rewrite during split
- `handleUnderflow(path)` → remove empty leaf, collapse root if needed

### bufferpool.go

**LRU page cache with pin counting.**

Type: `BufferPool` — holds `*Pager`, `*FreeList`, capacity, page map, LRU doubly-linked list
Type: `bufferEntry` — page + pinCount + prev/next pointers

Functions:
- `NewBufferPool(pager, freelist, capacity)` → default capacity 1024
- `FetchPage(pageID)` → cache hit or disk read, returns pinned page
- `NewPage(pageType)` → allocate via freelist, returns pinned page
- `Unpin(pageID)` → decrement pin count
- `MarkDirty(pageID)` → flag page for flush
- `FlushPage(pageID)` → write dirty page to disk
- `FlushAll()` → write all dirty pages + fsync
- `FreePage(pageID)` → remove from cache, return to freelist

### freelist.go

**Free page management.**

Type: `FreeList` — holds `*Pager`, `sync.Mutex`

Constants: `FreeListHeaderSize = 7`, `MaxFreeListEntries = 1022`

Functions:
- `AllocatePage()` → pop from freelist head, or allocate new from pager
- `FreePage(pageID)` → push onto freelist head, create new freelist page if full

### wal.go

**Write-ahead log for crash recovery.**

Types:
- `WALRecord` — LSN, TxID, Type (PageWrite/Commit/Checkpoint), PageID, Data, CRC
- `WAL` — holds file, path, nextLSN, nextTxID, mutex

Functions:
- `OpenWAL(path)` → open file, scan existing records to find highest LSN/TxID
- `BeginTx()` → return new transaction ID
- `LogPageWrite(txID, pageID, data)` → append record, fsync, return LSN
- `LogCommit(txID)` → append commit record, fsync
- `LogCheckpoint()` → append checkpoint record
- `Recover()` → scan records, return `map[pageID]data` for committed txns after last checkpoint
- `Truncate()` → clear the WAL file
- `Close()` → close file

---

## internal/table/

### table.go

**Table abstraction wrapping B+ tree.**

Type: `Table` — holds name, tree, pool, pager, freelist, wal, dataDir, mutex

Functions:
- `OpenTable(name, opts)` → open pager + WAL, recover, open B+ tree
- `GetItem(key)` → search tree, JSON unmarshal
- `PutItem(key, doc)` → JSON marshal, insert into tree
- `DeleteItem(key)` → delete from tree
- `Scan(fn)` → iterate all items in key order
- `Count()` → count items
- `Close()` → flush tree, close WAL, close pager

### catalog.go

**Table metadata management.**

Types:
- `TableMeta` — Name, CreatedAt, ItemCount
- `Catalog` — dataDir, tables map, mutex. Persisted as `catalog.json`
- `TableManager` — catalog, open table cache, lazy loading

Catalog functions:
- `OpenCatalog(dataDir)` → load or create catalog.json
- `Save()` → persist to disk
- `CreateTable(name)` / `DeleteTable(name)` / `GetTable(name)` / `ListTables()`

TableManager functions:
- `NewTableManager(catalog, opts)` → create manager
- `GetTable(name)` → lazy-open with double-check locking
- `CreateTable(name)` → catalog create + open table
- `DeleteTable(name)` → close + catalog delete + remove files
- `Close()` → close all open tables

### encoding.go

**Key encoding.** `EncodeKey(string) → []byte` and `DecodeKey([]byte) → string`. Raw UTF-8 bytes.

---

## internal/api/

### router.go

**Trie-based HTTP router.**

Types:
- `Router` — root trieNode, middleware slice
- `trieNode` — children map, param child, paramKey, handlers map
- `Middleware` — `func(http.Handler) http.Handler`

Functions:
- `NewRouter()` → create router
- `Handle(method, pattern, handler)` — register route with `{param}` support
- `GET/POST/PUT/DELETE(pattern, handler)` — convenience methods
- `Use(middleware)` — add middleware
- `ServeHTTP(w, r)` — apply middleware chain, dispatch
- `Param(r, key)` — extract path parameter from request context

### handlers.go

**API request handlers.**

Type: `Handlers` — holds `*TableManager`

Functions:
- `CreateTable(w, r)` — parse name from body, create table, return 201
- `ListTables(w, r)` — list all tables from catalog
- `DeleteTable(w, r)` — delete table by name from URL param
- `GetItem(w, r)` — get item by table name + key from URL params
- `PutItem(w, r)` — parse JSON body, put item
- `DeleteItem(w, r)` — delete item
- `ScanItems(w, r)` — scan all items in a table

Helpers: `writeJSON(w, status, data)`, `writeError(w, status, message)`

### middleware.go

**HTTP middleware stack.**

Functions:
- `LoggingMiddleware` — logs method, path, status, duration
- `RecoveryMiddleware` — catches panics, returns 500
- `CORSMiddleware` — adds CORS headers, handles OPTIONS

Type: `statusWriter` — wraps ResponseWriter to capture status code

### server.go

**HTTP server with graceful shutdown.**

Type: `Server` — holds httpServer, router, handlers, tableManager

Functions:
- `NewServer(bindAddr, tableManager)` — create server, apply middleware, register all routes
- `Router()` — return router for additional route registration
- `Start()` — ListenAndServe
- `Shutdown(ctx)` — graceful shutdown + close table manager

### webui.go

**Embedded web UI serving.**

Functions:
- `RegisterWebUI(router, webFS)` — serve embedded `web/dist/` files; `/assets/*` as static, `/` as SPA fallback

---

## internal/cluster/

### ring.go

**Consistent hashing with virtual nodes.**

Types:
- `VNode` — Hash uint32, NodeID string, VNodeIdx int
- `Ring` — sorted vnode slice, vnodeCount, physicalNodes set, mutex

Functions:
- `NewRing(vnodesPerNode)` → create ring (default 128)
- `AddNode(nodeID)` → create 128 vnodes, sort
- `RemoveNode(nodeID)` → filter out vnodes
- `GetNode(key)` → hash key, binary search ring, return primary node
- `GetNodes(key, n)` → walk ring clockwise, collect n distinct physical nodes
- `Nodes()` → sorted list of physical node IDs
- `Size()` → number of physical nodes
- `hashKey(key)` → FNV-1a hash

### membership.go

**Heartbeat-based failure detection.**

Types:
- `NodeStatus` — Alive(0), Suspect(1), Dead(2)
- `NodeInfo` — ID, Addr, Status, LastSeen
- `Membership` — selfID, selfAddr, nodes map, ring, heartbeat/failure config, HTTP client

Functions:
- `NewMembership(selfID, selfAddr, ring, heartbeatInterval, failureTimeout)` → create + add self
- `Start()` → launch heartbeat and failure detection goroutines
- `Stop()` → stop goroutines
- `AddNode(id, addr)` → add to membership + ring
- `HandleHeartbeat(fromID, fromAddr)` → update last seen, auto-discover unknown nodes
- `GetAliveNodes()` / `GetAllNodes()` / `IsAlive(nodeID)` / `GetNodeAddr(nodeID)` — queries

### coordinator.go

**Request routing and forwarding.**

Type: `Coordinator` — holds membership, ring, tableManager, replicator, quorum config, HTTP client

Functions:
- `NewCoordinator(membership, ring, tm, replN, readQ, writeQ)` → create
- `SetReplicator(r)` → set replicator (breaks circular dependency)
- `RouteGet(table, key, consistency)` → quorum read or local read + forward
- `RoutePut(table, key, doc, consistency)` → quorum write or local write + async replicate
- `RouteDelete(table, key, consistency)` → same pattern as put
- `RegisterInternalRoutes(router)` → register `/internal/v1/heartbeat` and `/internal/v1/replicate`

Internal: `quorumRead`, `quorumWrite`, `quorumDelete`, `forwardGet`, `forwardPut`, `forwardDelete`

### replication.go

**Async replication with hinted handoff.**

Types:
- `HintedHandoff` — map[nodeID][]hint, mutex
- `hint` — TableName, Key, Op, Doc, Timestamp
- `Replicator` — membership, handoff, HTTP client

Functions:
- `ReplicateWrite(table, key, doc, nodes)` → send to each node, store hint if down
- `ReplicateDelete(table, key, nodes)` → same for deletes
- `ReplayHints(nodeID)` → drain and replay hints for a recovered node

### antientropy.go

**Periodic Merkle tree sync.**

Types:
- `merkleKV` — key string, value []byte
- `MerkleNode` — Hash, KeyRange, Left, Right, IsLeaf, Keys
- `AntiEntropy` — membership, ring, tableManager, interval, HTTP client

Functions:
- `NewAntiEntropy(membership, ring, tm, interval)` → create
- `Start()` / `Stop()` → periodic loop control
- `BuildMerkleTree(table)` → construct Merkle tree over table keys
- `RegisterRoutes(router)` → register `/internal/v1/anti-entropy`
- `syncTableWithNode(tableName, node)` → compare key hashes, send divergent keys

---

## internal/consistency/

### vectorclock.go

**Vector clocks for causal ordering.**

Type: `VectorClock` — Clocks map[string]uint64, Timestamp time.Time, mutex

Functions:
- `NewVectorClock()` → create empty
- `Increment(nodeID)` → bump clock for node
- `Merge(other)` → take max of each component
- `Compare(other)` → returns -1 (before), 0 (concurrent), 1 (after)
- `Clone()` → deep copy
- `Serialize()` / `DeserializeVectorClock(data)` → JSON
- `ResolveConcurrent(a, b)` → LWW fallback using wall clock

### quorum.go

**Quorum configuration.**

Type: `QuorumConfig` — N, R, W

Functions:
- `NewQuorumConfig(n, r, w)` → validate and create
- `IsStrong()` → returns R+W>N
- `String()` → human-readable description

---

## internal/config/config.go

**Server configuration.**

Type: `Config` — NodeID, BindAddr, DataDir, SeedNodes, VirtualNodes, ReplicationN, ReadQuorum, WriteQuorum, BufferPoolSize, HeartbeatIntervalMS, FailureTimeoutMS, AntiEntropyIntervalSec

Functions:
- `DefaultConfig()` → sensible defaults
- `LoadConfig(path)` → read JSON file
- `Validate()` → check required fields and constraints

---

## test/integration_test.go

**HTTP API integration tests** using `httptest.Server`.

Tests: `TestAPI_TableCRUD`, `TestAPI_ItemCRUD`, `TestAPI_ScanItems`, `TestAPI_NonExistentTable`, `TestAPI_Health`

Helpers: `setupTestServer(t)`, `doGet`, `doPost`, `doPut`, `doDelete`

---

## scripts/

| File | Description |
|------|-------------|
| `start-cluster.sh` | Starts 3 nodes on :8081-8083 with data in `./data/node{1,2,3}/` |
| `seed-data.sh` | Creates users/products/orders tables with sample data |

---

## web/

| File | Description |
|------|-------------|
| `vite.config.ts` | Vite config: React plugin, Tailwind plugin, API proxy to :8081 |
| `src/main.tsx` | React entry point, renders `<App />` |
| `src/App.tsx` | Main app: header nav, page routing via state |
| `src/index.css` | Tailwind import |
| `src/lib/api.ts` | Typed API client with fetch wrapper |
| `src/pages/Dashboard.tsx` | Stats, cluster config, table list |
| `src/pages/Tables.tsx` | Table create/delete, click to detail |
| `src/pages/TableDetail.tsx` | Item CRUD with JSON editor + edit modal |
| `src/pages/ClusterStatus.tsx` | Ring SVG visualization, node status table, auto-refresh |
