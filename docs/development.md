# Development Guide

## Prerequisites

- Go 1.21+ (`go version`)
- Node.js 18+ and npm (`node --version`, `npm --version`)

No external Go dependencies. The entire database engine uses only the standard library.

## Build

```bash
make build          # Build both binaries to ./bin/
make clean          # Remove binaries and data directories
```

Outputs:
- `./bin/hedgehogdb` — server binary
- `./bin/hedgehogctl` — CLI tool
- `./bin/trafficgen` — live traffic generator for demo tables

## Run

### Single Node

```bash
make dev
# or
./bin/hedgehogdb -node-id node-1 -bind 0.0.0.0:8080 -data-dir ./data
```

### 3-Node Cluster

```bash
make cluster
# or
bash scripts/start-cluster.sh
```

Starts nodes on ports 8081, 8082, 8083 with data in `./data/node{1,2,3}/`.

### Seed Sample Data

```bash
make seed
# or
HEDGEHOG_URL=http://localhost:8081 bash scripts/seed-data.sh
```

Creates tables: users (20 items), products (10 items), orders (15 items).

### Live Traffic Generator

Generate configurable insert/update/delete load against the three demo tables (users, products, orders). Default rates keep **inserts > updates + deletes** so data grows over time.

```bash
./bin/trafficgen
# or with custom rates
./bin/trafficgen -inserts 10 -updates 3 -deletes 1
# single node
./bin/trafficgen   # default: http://localhost:8081 (first cluster node)
# round-robin across cluster nodes
./bin/trafficgen -urls http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083
```

Flags:
- `-url` — base URL when not using `-urls` (default: `http://localhost:8081`, first cluster node; or `HEDGEHOG_URL`)
- `-urls` — comma-separated node URLs to round-robin across (overrides `-url` for traffic; table create/scan use first URL)
- `-inserts` — inserts per second (default: 5; keep > updates+deletes so data grows)
- `-updates` — updates per second (default: 2)
- `-deletes` — deletes per second (default: 1)

At startup the tool logs **which node(s)** it is hitting. With a **single URL** (e.g. default 8081), it discovers the full cluster via `GET /api/v1/cluster/status` and then **sends traffic to all nodes** (round-robin), so table counts grow on every node. It also creates the demo tables on every node so node 2 and 3 show tables in the UI. Stats are logged every 5 seconds. Stop with Ctrl+C.

**Verify traffic on all nodes:**

```bash
# API: per-node table counts (call from any node)
curl http://localhost:8081/api/v1/cluster/table-counts

# Script: run trafficgen 15s and assert all nodes have table counts
./scripts/verify-traffic.sh http://localhost:8081
```

Response shape: `{"users":{"node-1":100,"node-2":95,...},"products":{...},"orders":{...}}`. Use this to confirm tables exist on all nodes and counts are increasing.

## Test

```bash
make test           # All tests
make test-race      # With race detector
make bench          # Storage engine benchmarks
go test -v ./internal/storage/...     # Storage tests only
go test -v ./internal/cluster/...     # Cluster tests only
go test -v ./internal/consistency/... # Consistency tests only
go test -v ./test/...                 # Integration tests
```

### Test Coverage

| Package | Tests | What's Tested |
|---------|-------|---------------|
| `internal/storage` | 18 | Page serialization, CRC integrity, leaf/internal cell operations, pager create/reopen, B+ tree insert/search/delete with 1000 keys, scan ordering, split/merge, WAL write/recover/checkpoint |
| `internal/cluster` | 6 | Ring add/remove/get, N-replica selection, distribution balance, consistency after node changes |
| `internal/consistency` | 7 | Vector clock increment/compare/merge/clone/serialize, quorum config validation |
| `test` | 5 | Full HTTP API lifecycle via httptest: table CRUD, item CRUD with update, scan, 404 handling, health check |

### Writing New Tests

Storage tests use a helper that creates a temp directory and cleans up:

```go
func setupTestTree(t *testing.T) (*BPlusTree, func()) {
    dir, _ := os.MkdirTemp("", "hedgehog-test-*")
    pager, _ := OpenPager(filepath.Join(dir, "test.db"))
    freelist := NewFreeList(pager)
    pool := NewBufferPool(pager, freelist, 256)
    tree, _ := OpenBPlusTree(pool, pager, 0)
    cleanup := func() { tree.Close(); pager.Close(); os.RemoveAll(dir) }
    return tree, cleanup
}
```

Integration tests use httptest:

```go
func setupTestServer(t *testing.T) (*httptest.Server, func()) {
    dir, _ := os.MkdirTemp("", "hedgehog-integration-*")
    catalog, _ := table.OpenCatalog(dir)
    tm := table.NewTableManager(catalog, table.TableOptions{DataDir: dir, BufferPoolSize: 256})
    server := api.NewServer(":0", tm)
    ts := httptest.NewServer(server.Router())
    cleanup := func() { ts.Close(); tm.Close(); os.RemoveAll(dir) }
    return ts, cleanup
}
```

## CLI Flags

```
./bin/hedgehogdb [flags]

  -bind string        Bind address (default "0.0.0.0:8080")
  -config string      Path to JSON config file
  -data-dir string    Data directory (default "./data")
  -node-id string     Unique node identifier (default "node-1")
  -seed-nodes string  Comma-separated seed node addresses
```

## Configuration File

All flags can be set via a JSON config file:

```json
{
  "node_id": "node-1",
  "bind_addr": "0.0.0.0:8080",
  "data_dir": "./data",
  "seed_nodes": ["127.0.0.1:8081"],
  "virtual_nodes": 128,
  "replication_n": 3,
  "read_quorum": 2,
  "write_quorum": 2,
  "buffer_pool_size": 1024,
  "heartbeat_interval_ms": 1000,
  "failure_timeout_ms": 5000,
  "anti_entropy_interval_sec": 300
}
```

## Debugging

### Inspect the Database File

The `.db` file is binary. To understand its structure:
- Bytes 0–7: should be `HEDGEHOG` (magic)
- Bytes 8–11: version (uint32 LE, should be 1)
- Bytes 12–15: root page ID
- Bytes 16–19: total pages
- Each subsequent 4096-byte block is a page
- Page byte 0: type (0x01=internal, 0x02=leaf)
- Page bytes 2–3: cell count

```bash
xxd -l 36 data/node1/users.db   # View file header
xxd -s 4096 -l 16 data/node1/users.db  # View page 1 header
```

### Common Issues

**"all pages are pinned, cannot evict"**: The buffer pool is full and every page is in use. Increase `buffer_pool_size` or ensure code paths always call `Unpin()` after `FetchPage()`.

**"cannot read page 0 as a data page"**: Page 0 is the file header. Data pages start at ID 1.

**WAL grows without bound**: Call checkpoint periodically. Currently checkpointing is manual — no automatic checkpoint timer exists.

**Port already in use**: Kill existing processes: `pkill hedgehogdb`

### Log Output

The server logs every request with method, path, status code, and duration:
```
POST /api/v1/tables 201 504.917µs
GET /api/v1/tables/users/items/alice 200 17.667µs
```

Cluster events are also logged:
```
Node node-2 (127.0.0.1:8082) joined the cluster
Node node-3 is suspect (no heartbeat for 5s)
Node node-3 declared dead
```

## Extending the Codebase

### Adding a New API Endpoint

1. Add handler method to `internal/api/handlers.go`
2. Register route in `internal/api/server.go:NewServer()`
3. Add integration test in `test/integration_test.go`

### Adding a New Storage Feature

1. Implement in `internal/storage/`
2. Add unit test in the same package
3. If it changes the page format, update the file version in `page.go`

### Adding a New Cluster Feature

1. Implement in `internal/cluster/`
2. If it needs HTTP endpoints, register in `coordinator.go:RegisterInternalRoutes()` or create a new `RegisterRoutes()` method
3. Wire it up in `cmd/hedgehogdb/main.go`

### Adding a New Web UI Page

1. Create `web/src/pages/NewPage.tsx`
2. Add page key to the `Page` type union in `App.tsx`
3. Add nav button and conditional render in `App.tsx`
4. Add any needed API methods to `web/src/lib/api.ts`

## Known Limitations & Future Work

### Storage Engine
- **No overflow page integration**: Values > ~1.5KB may fail to insert. The overflow page format exists but isn't wired into B+ tree insert/search.
- **Simplified delete**: Underflow handling removes empty leaves and collapses root, but doesn't redistribute keys between siblings or merge non-empty underfull nodes.
- **No page compaction**: Deleted cells leave dead space in pages. Space is only reclaimed during page splits (which rebuild the page).
- **WAL not fully integrated**: WAL infrastructure exists but B+ tree writes go directly to disk without logging. Full crash safety requires routing writes through the WAL.

### Cluster
- **Hinted handoff in memory only**: Hints are lost on restart. Should be persisted to disk.
- **No partition detection**: Network partitions are handled as node failures, not as split-brain scenarios.
- **Vector clocks not integrated**: The types exist but items are stored without version metadata.
- **Anti-entropy uses key-hash comparison**: Works but O(n) in number of keys. Full Merkle tree exchange would reduce bandwidth for large tables with few differences.

### Web UI
- **No URL routing**: Browser back/forward doesn't work. Could add hash-based or pushState routing.
- **No pagination**: Scan loads all items at once. Large tables will be slow.
- **No authentication**: The API is completely open. Add auth middleware for production use.
