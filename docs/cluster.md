# Cluster & Distribution

## Consistent Hashing

**File**: `internal/cluster/ring.go`

### How It Works

The consistent hash ring maps keys to nodes using FNV-1a hashing:

```
Physical Node "node-1" → 128 virtual nodes:
  hash("node-1#0")   = 0x1A3F...  → placed on ring
  hash("node-1#1")   = 0x7B2C...  → placed on ring
  ...
  hash("node-1#127") = 0xE4D1...  → placed on ring
```

To find the node for a key:
```
1. hash(key) → uint32
2. Binary search the sorted vnode list for the first vnode with hash ≥ key_hash
3. If past the end, wrap to index 0 (ring wraps around)
4. Return that vnode's physical node ID
```

### Replication: GetNodes(key, N)

To find N replica nodes:
```
1. Find the primary node (as above)
2. Continue walking clockwise through vnodes
3. Collect distinct physical node IDs until we have N
4. Skip duplicate physical nodes (multiple vnodes of the same node)
```

This ensures replicas are on different physical nodes.

### Node Addition/Removal

- **AddNode**: Creates 128 vnodes, inserts into sorted list, re-sorts
- **RemoveNode**: Filters out all vnodes for that physical node

Consistent hashing property: when a node is added/removed, only ~1/N of keys change ownership (where N = number of nodes).

### Configuration

- Default: 128 vnodes per physical node
- Configurable via `virtual_nodes` in config
- More vnodes = better balance but more memory

---

## Membership & Failure Detection

**File**: `internal/cluster/membership.go`

### Node States

```
ALIVE ──(no heartbeat for 5s)──→ SUSPECT ──(no heartbeat for 10s)──→ DEAD
  ↑                                  │
  └──────(heartbeat received)────────┘
```

| State | Description |
|-------|-------------|
| Alive (0) | Node is responding to heartbeats |
| Suspect (1) | Missed heartbeats, might be down |
| Dead (2) | Confirmed down, removed from ring |

### Heartbeat Protocol

```
Every 1 second (configurable via heartbeat_interval_ms):
  For each known non-dead node (except self):
    POST http://{node_addr}/internal/v1/heartbeat
    Body: {"node_id": "self-id", "addr": "self-addr"}
```

When a heartbeat is received:
- If node is known: update LastSeen, set status to Alive
- If node is unknown: add to membership, add to ring (auto-discovery)

### Failure Detection Loop

```
Every heartbeat interval:
  For each known node (except self):
    elapsed = now - LastSeen
    If Alive and elapsed > failure_timeout: → Suspect
    If Suspect and elapsed > 2 * failure_timeout: → Dead, remove from ring
```

### Node Join

```
1. New node starts with seed_nodes configured
2. Adds seed nodes to its membership
3. Starts sending heartbeats to seed nodes
4. Seed nodes discover the new node via heartbeat → add to their membership
5. Through heartbeat gossip, all nodes eventually discover the new node
```

---

## Request Coordination

**File**: `internal/cluster/coordinator.go`

The coordinator routes client requests to the appropriate replica nodes.

### Write Path (PUT)

```
RoutePut(table, key, doc, consistency):
  1. nodes = Ring.GetNodes(key, N)        # Find N replica nodes
  2. If this node is a replica:
       Write locally: Table.PutItem(key, doc)
  3. If strong consistency:
       Send PUT to all N nodes in parallel
       Wait for W acknowledgements
       Return success if W met, error otherwise
  4. If eventual consistency:
       Async replicate to other nodes via Replicator
       If this node is NOT a replica: forward to primary
       Return success immediately
```

### Read Path (GET)

```
RouteGet(table, key, consistency):
  1. nodes = Ring.GetNodes(key, N)
  2. If strong consistency:
       Query all N nodes in parallel
       Wait for R responses
       Return the latest value (by vector clock, if implemented)
  3. If eventual consistency:
       If this node is a replica: read locally
       Otherwise: forward to primary node
```

### Delete Path

Same pattern as write, but calls `Table.DeleteItem()`.

### Forwarding

When this node is not a replica for the key, the request is forwarded via HTTP to the appropriate node using the `/internal/v1/replicate` endpoint.

---

## Replication

**File**: `internal/cluster/replication.go`

### Async Replication

For eventual consistency writes:
```
ReplicateWrite(table, key, doc, nodes):
  For each node in nodes (except self):
    If node is alive:
      POST /internal/v1/replicate?table=X&key=Y&op=put  (body = doc)
    If node is down:
      Store hint in HintedHandoff
```

### Hinted Handoff

When a replica node is down, writes destined for it are stored locally:

```go
type hint struct {
    TableName string
    Key       string
    Op        string              // "put" or "delete"
    Doc       map[string]interface{}
    Timestamp time.Time
}
```

Hints are stored in memory (map[nodeID][]hint). When a node recovers (detected via heartbeats resuming), hints are replayed:

```
ReplayHints(nodeID):
  hints = DrainHints(nodeID)
  For each hint:
    Forward to the recovered node
    If forward fails: re-add hint for later retry
```

**Current limitation**: Hints are stored in memory only. If the hinting node restarts, hints are lost. For production use, hints should be persisted to disk.

---

## Anti-Entropy

**File**: `internal/cluster/antientropy.go`

### Purpose

Anti-entropy catches any inconsistencies that slipped through (network issues, missed hints, etc.) by periodically comparing data between replicas.

### Protocol

```
Every 5 minutes (configurable via anti_entropy_interval_sec):
  For each table:
    For each alive peer node:
      1. Scan local table → collect {key: hash(value)} for all keys
      2. GET /internal/v1/anti-entropy?table=X from peer → get their {key: hash(value)}
      3. Compare:
         - Keys where local hash ≠ remote hash → send local version to peer
         - Keys that exist locally but not on peer → send to peer
      4. POST /internal/v1/replicate for each divergent key
```

### Merkle Tree (Partial Implementation)

`BuildMerkleTree()` constructs a Merkle tree over a table's keys for efficient comparison:

```
- Leaf nodes: groups of up to 16 keys, hashed together (SHA-256)
- Internal nodes: hash of child hashes
- Root hash comparison: if roots match, tables are identical
- If roots differ: recurse into children to find divergent key ranges
```

The current anti-entropy implementation uses the simpler key-hash comparison approach rather than the full Merkle tree exchange, but the tree construction code is available for future optimization.

---

## Consistency Guarantees

**File**: `internal/consistency/quorum.go`, `vectorclock.go`

### Quorum Configuration

| Parameter | Default | Meaning |
|-----------|---------|---------|
| N | 3 | Replication factor (data stored on N nodes) |
| R | 2 | Read quorum (wait for R responses on strong read) |
| W | 2 | Write quorum (wait for W acks on strong write) |

**Strong consistency**: R + W > N (default: 2 + 2 = 4 > 3). Any read quorum and write quorum must overlap by at least one node, guaranteeing the read sees the latest write.

**Eventual consistency**: Client gets immediate response after local write; replication happens asynchronously.

### Vector Clocks

Each write can be tagged with a vector clock for causal ordering:

```go
type VectorClock struct {
    Clocks    map[string]uint64  // nodeID → logical clock
    Timestamp time.Time          // wall clock for LWW fallback
}
```

**Compare** returns:
- `-1`: a happened-before b
- `1`: a happened-after b
- `0`: concurrent (neither dominates)

**Conflict resolution for concurrent writes**: Last-Writer-Wins (LWW) using wall clock timestamp.

**Current status**: Vector clock types and comparison logic are implemented. They are not yet integrated into the replication path — items are currently stored without version metadata. Integration would require storing the vector clock alongside each item's value.

---

## Cluster Topology Example

```
3-node cluster with N=3, R=2, W=2:

Node 1 (:8081) ←──heartbeat──→ Node 2 (:8082)
    ↕                                ↕
    └────────heartbeat──────→ Node 3 (:8083)

For key "alice" (hash = 0x3F...):
  Ring.GetNodes("alice", 3) → [node-2, node-3, node-1]
  Primary coordinator: node-2
  Replicas: node-3, node-1

PUT "alice" with strong consistency:
  → Write to node-2 (primary)
  → Write to node-3 (replica)
  → Write to node-1 (replica)
  → Wait for 2 acks → success

If node-3 is down:
  → Write to node-2 ✓
  → Write to node-3 ✗ → store hint
  → Write to node-1 ✓
  → 2 acks received → success
  → When node-3 recovers: replay hint
```
