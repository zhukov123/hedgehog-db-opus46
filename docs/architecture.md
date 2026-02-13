# Architecture Overview

## System Layers

```
┌─────────────────────────────────────────────────┐
│                   Web UI (React)                │
│         Dashboard / Tables / Cluster            │
├─────────────────────────────────────────────────┤
│                  HTTP API Layer                  │
│   Router → Middleware → Handlers → Response      │
├─────────────────────────────────────────────────┤
│              Cluster Coordination                │
│  Ring │ Membership │ Coordinator │ Replication    │
├─────────────────────────────────────────────────┤
│                 Table Layer                       │
│       Table Manager → Table → B+ Tree            │
├─────────────────────────────────────────────────┤
│               Storage Engine                      │
│   BPlusTree → BufferPool → Pager → Disk          │
│         WAL │ FreeList │ Pages                    │
└─────────────────────────────────────────────────┘
```

## Component Relationships

### Request Flow (single-node PUT)

```
Client PUT /api/v1/tables/users/items/alice
  → Router.dispatch()          # trie match, extract {name}="users", {key}="alice"
  → CORSMiddleware             # add CORS headers
  → LoggingMiddleware           # log request
  → RecoveryMiddleware          # catch panics
  → Handlers.PutItem()         # parse JSON body
    → TableManager.GetTable()  # lazy-open table (or return cached)
      → Table.PutItem()        # JSON marshal, encode key
        → BPlusTree.Insert()   # find leaf, insert or split
          → BufferPool.FetchPage()  # check LRU cache or read from disk
            → Pager.ReadPage()      # disk I/O at page_id * 4096
          → Page.InsertLeafCell()   # binary search, write cell, update pointers
          → BufferPool.FlushPage()  # write dirty page back to disk
            → Pager.WritePage()
```

### Request Flow (cluster PUT with quorum)

```
Client PUT to any node
  → Coordinator.RoutePut()
    → Ring.GetNodes(key, N=3)   # find 3 responsible nodes via consistent hash
    → If strong consistency:
        → Send PUT to all 3 nodes in parallel
        → Wait for W=2 acknowledgements
        → Return success
    → If eventual consistency:
        → Write locally (if this node is a replica)
        → Async replicate to other nodes via Replicator
        → If a node is down: store in HintedHandoff
```

## Concurrency Model

- **BPlusTree**: `sync.RWMutex` — reads are concurrent, writes are serialized
- **BufferPool**: `sync.Mutex` — all cache operations are serialized; individual pages are safe because the B+ tree lock prevents concurrent modification of the same page
- **Pager**: `sync.RWMutex` — reads are concurrent, writes (including allocate) are serialized
- **FreeList**: `sync.Mutex` — allocate/free are serialized
- **WAL**: `sync.Mutex` — all log appends are serialized with fsync
- **Table**: `sync.RWMutex` — wraps the B+ tree operations
- **TableManager**: `sync.RWMutex` — protects the open table cache
- **Catalog**: `sync.RWMutex` — protects table metadata map
- **Ring**: `sync.RWMutex` — protects vnode slice and physical node map
- **Membership**: `sync.RWMutex` — protects node info map

## Data Persistence

Each table is stored in two files:
- `{table_name}.db` — The B+ tree data file (pages)
- `{table_name}.wal` — Write-ahead log for crash recovery

The catalog is stored as `catalog.json` in the data directory.

### Crash Recovery Sequence

```
1. Open WAL file
2. Scan all records
3. Build map of committed transactions (have both PageWrite and Commit records)
4. Discard uncommitted transactions
5. Apply committed page writes to the .db file
6. Sync .db file
7. Truncate WAL
8. Open B+ tree normally
```

## Startup Sequence (cmd/hedgehogdb/main.go)

```
1. Parse flags / load config
2. Create data directory
3. Open catalog (catalog.json)
4. Create TableManager (lazy table opening)
5. Create consistent hash Ring
6. Create Membership manager (adds self to ring)
7. Add seed nodes to membership
8. Create Coordinator (routes requests)
9. Create Replicator (async replication + hinted handoff)
10. Create AntiEntropy service (periodic Merkle sync)
11. Create API Server with middleware stack
12. Register all routes (API + internal + cluster + web UI)
13. Start Membership heartbeat loop
14. Start AntiEntropy periodic loop
15. Start HTTP server
16. Wait for SIGINT/SIGTERM
17. Graceful shutdown (stop services, flush tables, close server)
```

## Shutdown Sequence

```
1. Receive signal
2. Stop membership heartbeats
3. Stop anti-entropy loop
4. HTTP server graceful shutdown (drain connections, 10s timeout)
5. TableManager.Close() → each Table.Close() → BPlusTree.Close() → BufferPool.FlushAll()
6. WAL.Close()
7. Pager.Sync() + Pager.Close()
```
