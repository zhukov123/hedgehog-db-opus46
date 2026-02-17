# Write Performance Roadmap

## Current State (Feb 2026)

- **Write throughput**: ~120 writes/s aggregate across 3 tables (~40/s per table)
- **Read throughput**: ~6,500 reads/s (scales linearly with load)
- **Write p50 latency**: degrades from 44ms to 1.4s as tree grows
- **Write p99 latency**: degrades from 230ms to 4.8s
- **Read p50 latency**: stable at 11-14ms throughout
- **Error rate**: zero 5xx errors under stress up to 16,800 req/s requested

## Baseline: write-stress (p95 ≤ 300 ms)

Measured before any write-performance changes. Use this to validate improvements.

**Command:**
```bash
./bin/trafficgen -write-stress -write-stress-p95-limit 300 \
  -write-stress-initial 50 -write-stress-step 25 -write-stress-duration 20s \
  -urls "http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083"
```

**Single-run result (16 Feb 2026):**
- **Max write rate before p95 exceeded 300 ms:** **225 strong writes/s** (cluster, 3 nodes round-robin)
- At 250 writes/s, p95 = 370 ms (stopped); at 225 writes/s, p95 = 288 ms (last passing step)
- Errors: 0 (when using only the 3 cluster nodes; avoid including a down node in `-urls` or cluster discovery will send ~25% of traffic to it)

**Repeated runs (same day, 3 runs):**
| Run | Max rate (p95 ≤ 300 ms) | Stopped at (p95) | Note |
|-----|-------------------------|------------------|------|
| 1   | **700** writes/s        | 725/s → p95 328 ms | Smaller trees (~150k keys/table) |
| 2   | **100** writes/s       | 125/s → p95 377 ms | Right after run 1; trees large, high contention |
| 3   | **675** writes/s       | 700/s → p95 308 ms | Similar to run 1 |
- **Conclusion:** Numbers vary a lot with tree size and recent load. Use **~100–225 writes/s** as a conservative baseline for “large tree” conditions; **~675–700 writes/s** when trees are smaller. For before/after comparison, run multiple times or fix tree size (e.g. fresh data dir or same key count).

**Double-check in Grafana:** Start the observability stack (`./scripts/start-observability.sh`), then open **http://localhost:3001/d/hedgehogdb-overview**. During or after write-stress: **Request rate** = `sum(rate(hedgehog_http_requests_total[1m])) by (operation)` (look for `put`); **Latency p95** = `histogram_quantile(0.95, sum(rate(hedgehog_http_request_duration_seconds_bucket[1m])) by (le))` (in seconds; multiply by 1000 for ms). Prometheus: http://localhost:9090.

Re-run the same trafficgen command after changes to compare.

## After Option 1 (16 Feb 2026)

**Implemented:**
- **Immediate commit:** WAL group-commit loop waits only 50µs when a single commit is pending; if no second request arrives, sync immediately instead of 2ms. Reduces latency for the single-writer case.
- **Avoid double leaf fetch:** `findLeafPathWithLeaf` returns the leaf page; insert/delete and split use it instead of calling `FetchPage(leafID)` again after `findLeafPath`.

**Write-stress (3 rounds, same command as baseline):**
| Round | Max rate (p95 ≤ 300 ms) | Stopped at (p95) |
|-------|-------------------------|------------------|
| 1     | **725** writes/s        | 750/s → p95 319 ms |
| 2     | **700** writes/s        | 725/s → p95 exceeded |
| 3     | (run in progress when captured) | — |
- **Conclusion:** Option 1 keeps throughput in the **700–725 writes/s** range (similar to pre–Option 1 best runs). The main gain is lower latency when only one writer is active (no 2ms group-commit wait) and one fewer `FetchPage` per insert on the hot path.

## After Option 2 (16 Feb 2026)

**Implemented:** Table-level key partitioning with 8 partitions per table (configurable via `partition_count`). Each partition has its own B+ tree, WAL, and lock; keys are routed by `hash(key) % N`. Scans merge across partitions via sorted min-heap.

**To test with partitioning:** Use a fresh cluster so new tables are created with partition files:
```bash
rm -rf data/
./scripts/start-cluster.sh   # in one terminal
# Wait for cluster to be ready, then:
./bin/trafficgen -write-stress -write-stress-p95-limit 300 \
  -write-stress-initial 50 -write-stress-step 25 -write-stress-duration 20s \
  -urls "http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083"
```

**Note:** Existing tables (e.g. `users.db`) use the legacy single-file layout for backward compatibility. Only newly created tables after enabling partitioning use `{name}_p0.db` … `{name}_p7.db`.

**Stress test (fresh cluster, partitioned layout, 16 Feb 2026):**
| Round | Keys/table (start) | Max write rate (p95 ≤ 300 ms) |
|-------|--------------------|-------------------------------|
| 1     | 0                  | **750** strong writes/s       |
| 2     | ~28k               | **825** strong writes/s       |
| 3     | ~46k               | (cluster timeouts)            |
- **Before (Option 1, non-partitioned):** 625–725 writes/s
- **After (Option 2, partitioned):** 750–825 writes/s → **~12–15% gain**, and round 2 shows partitioning *holds* throughput as trees grow (825 with 28k keys vs 625 with similar load before)

## Bottleneck

Every write acquires an exclusive lock on the B+ tree (`tree.mu.Lock()` in `btree.go`). Only one write can be inside the tree at a time per table. The critical path under the lock:

```
tree.mu.Lock() → findLeafPath → insert/split → WAL page writes → group commit (2ms wait) → tree.mu.Unlock()
```

As the tree grows deeper and splits more often, each write holds the lock longer and all other writes queue behind it.

## Options (ordered by effort)

### 1. Optimize the critical path (easy, ~2-3x) — *partially done*

- ~~Add "immediate commit" to group commit when only one writer is pending~~ → done (50µs window, then sync)
- ~~Avoid re-fetching leaf after `findLeafPath`~~ → done (`findLeafPathWithLeaf`, reuse in insert/delete/split)
- Profile the insert path to find allocations and hot spots
- Optimize `findLeafPath` to reduce repeated `FetchPage` overhead (e.g. path prealloc)
- Pre-allocate page buffers to reduce GC pressure
- Consider caching the root-to-leaf path for sequential inserts

### 2. Table-level key partitioning (moderate, ~4-8x) — *done*

Split each table into N sub-trees (e.g., 8), each with its own B+ tree, WAL, and lock. Route keys by hash to a partition. Writes to different partitions proceed in parallel.

- Implemented: `PartitionCount` (default 8) in config; hash(key) % N routing; per-partition B+ tree, WAL, lock
- Backward compat: existing `{name}.db` tables use legacy single-partition layout
- New tables use `{name}_p0.db` … `{name}_p7.db`; scans merge across partitions (min-heap sorted merge)
- Set `partition_count` in config or use default 8. Restart cluster with fresh `data/` to create partitioned tables.

### 3. Write batching at the API level (moderate, ~3-5x)

Buffer incoming writes and apply them in batches. One lock acquisition inserts N keys, amortizing lock overhead, tree traversal, and WAL commit.

- Adds small latency to individual writes (buffering window)
- Good synergy with group commit (batch tree ops + batch fsync)
- Could be combined with option 2 for multiplicative gains

### 4. Fine-grained page-level locking (hard, ~10x+)

Replace the tree-wide mutex with per-page latches (like InnoDB/PostgreSQL). Readers and writers only lock the pages they touch. Multiple concurrent writers can operate on different parts of the tree.

- Major architectural change to the B+ tree
- Need latch coupling (lock parent, lock child, release parent)
- Must handle deadlock avoidance for splits that propagate up
- Significant testing effort required

### 5. LSM-tree hybrid (redesign, order of magnitude)

Replace the B+ tree with a log-structured merge tree for write-heavy workloads. Writes go to an in-memory memtable and are flushed to sorted runs on disk. Background compaction merges runs.

- This is what LevelDB, RocksDB, Cassandra, and ScyllaDB use
- Writes become sequential I/O (very fast)
- Reads become slower (must check memtable + multiple levels)
- Complete redesign of the storage engine

## Recommended Order

Start with **option 1** (quick wins) then **option 2** (partitioning). Together they should reach 500-1000 writes/s per table without touching the B+ tree internals. Options 3-5 are for when those limits are reached.
