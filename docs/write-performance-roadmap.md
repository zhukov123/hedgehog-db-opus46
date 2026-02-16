# Write Performance Roadmap

## Current State (Feb 2026)

- **Write throughput**: ~120 writes/s aggregate across 3 tables (~40/s per table)
- **Read throughput**: ~6,500 reads/s (scales linearly with load)
- **Write p50 latency**: degrades from 44ms to 1.4s as tree grows
- **Write p99 latency**: degrades from 230ms to 4.8s
- **Read p50 latency**: stable at 11-14ms throughout
- **Error rate**: zero 5xx errors under stress up to 16,800 req/s requested

## Bottleneck

Every write acquires an exclusive lock on the B+ tree (`tree.mu.Lock()` in `btree.go`). Only one write can be inside the tree at a time per table. The critical path under the lock:

```
tree.mu.Lock() → findLeafPath → insert/split → WAL page writes → group commit (2ms wait) → tree.mu.Unlock()
```

As the tree grows deeper and splits more often, each write holds the lock longer and all other writes queue behind it.

## Options (ordered by effort)

### 1. Optimize the critical path (easy, ~2-3x)

- Profile the insert path to find allocations and hot spots
- Optimize `findLeafPath` to reduce repeated `FetchPage` overhead
- Add "immediate commit" to group commit when only one writer is pending (avoid the 2ms wait for nothing)
- Pre-allocate page buffers to reduce GC pressure
- Consider caching the root-to-leaf path for sequential inserts

### 2. Table-level key partitioning (moderate, ~4-8x)

Split each table into N sub-trees (e.g., 8), each with its own B+ tree, WAL, and lock. Route keys by hash to a partition. Writes to different partitions proceed in parallel.

- Clean, well-understood pattern (used by CockroachDB, Cassandra, etc.)
- Multiplies write throughput by partition count
- Doesn't change B+ tree internals
- Scans need to merge across partitions (sorted merge)
- Partition count could be configurable per table

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
