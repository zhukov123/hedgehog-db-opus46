# API Reference

Base URL: `http://localhost:8080` (configurable via `-bind` flag)

All request/response bodies are JSON. All responses include `Content-Type: application/json`.

---

## Health

### GET /api/v1/health

Check server health.

**Response** `200 OK`
```json
{
  "status": "ok"
}
```

---

## Tables

### POST /api/v1/tables

Create a new table.

**Request Body**
```json
{
  "name": "users"
}
```

**Response** `201 Created`
```json
{
  "name": "users",
  "message": "table created"
}
```

**Errors**
- `400` — missing or empty name
- `409` — table already exists

**What happens internally**: Catalog creates metadata entry → saves catalog.json → OpenTable creates `{name}.db` and `{name}.wal` files → initializes empty B+ tree with a root leaf page.

---

### GET /api/v1/tables

List all tables.

**Response** `200 OK`
```json
{
  "tables": [
    {
      "name": "users",
      "created_at": "2026-02-12T20:33:30.000178-05:00",
      "item_count": 0
    }
  ]
}
```

**Note**: `item_count` is from the catalog metadata and may not reflect the actual count in real-time. It's updated periodically, not on every write.

---

### DELETE /api/v1/tables/{name}

Delete a table and all its data.

**Response** `200 OK`
```json
{
  "message": "table \"users\" deleted"
}
```

**Errors**
- `404` — table not found

**What happens internally**: Close the Table (flush all pages) → remove from TableManager cache → delete from Catalog → save catalog.json → delete `{name}.db` and `{name}.wal` files.

---

## Items

### PUT /api/v1/tables/{name}/items/{key}

Create or update an item. The request body is the JSON document to store.

**Request Body** — any valid JSON object
```json
{
  "name": "Alice",
  "email": "alice@example.com",
  "age": 30
}
```

**Response** `200 OK`
```json
{
  "key": "alice",
  "message": "item saved"
}
```

**Errors**
- `400` — invalid JSON body
- `404` — table not found
- `500` — storage error (e.g., key too large)

**What happens internally**: JSON marshal the document → `BPlusTree.Insert(key_bytes, json_bytes)` → find leaf → if key exists, delete old cell and re-insert → if leaf is full, split and promote.

**Key constraints**: Max key size is 512 bytes. Max inline value size is ~2KB (limited by what fits in a leaf page cell alongside the key and cell pointer overhead). In practice, JSON documents up to ~1.5KB work reliably.

---

### GET /api/v1/tables/{name}/items/{key}

Get a single item by key.

**Query Parameters**
- `consistency` — `strong` or `eventual` (only meaningful in cluster mode, default: eventual)

**Response** `200 OK`
```json
{
  "key": "alice",
  "item": {
    "name": "Alice",
    "email": "alice@example.com",
    "age": 30
  }
}
```

**Errors**
- `404` — table not found, or item not found

---

### DELETE /api/v1/tables/{name}/items/{key}

Delete an item.

**Response** `200 OK`
```json
{
  "message": "item \"alice\" deleted"
}
```

**Errors**
- `404` — table or item not found

---

### GET /api/v1/tables/{name}/items

Scan all items in a table in sorted key order.

**Response** `200 OK`
```json
{
  "items": [
    {
      "key": "alice",
      "item": { "name": "Alice", "age": 30 }
    },
    {
      "key": "bob",
      "item": { "name": "Bob", "age": 25 }
    }
  ],
  "count": 2
}
```

**What happens internally**: `BPlusTree.Scan()` traverses the leaf chain from leftmost leaf, following RightPointer links, yielding all key-value pairs in sorted order.

---

## Cluster

### GET /api/v1/cluster/status

Get cluster overview.

**Response** `200 OK`
```json
{
  "node_id": "node-1",
  "bind_addr": "127.0.0.1:8081",
  "replication_n": 3,
  "read_quorum": 2,
  "write_quorum": 2,
  "total_nodes": 3,
  "ring_size": 3,
  "nodes": [
    {
      "id": "node-1",
      "addr": "127.0.0.1:8081",
      "status": 0,
      "last_seen": "2026-02-12T20:37:37.956119-05:00"
    },
    {
      "id": "node-2",
      "addr": "127.0.0.1:8082",
      "status": 0,
      "last_seen": "2026-02-12T20:37:37.891393-05:00"
    }
  ]
}
```

**Node status values**: `0` = alive, `1` = suspect, `2` = dead

---

### GET /api/v1/cluster/nodes

List all known nodes.

**Response** `200 OK`
```json
{
  "nodes": [
    { "id": "node-1", "addr": "127.0.0.1:8081", "status": 0, "last_seen": "..." }
  ]
}
```

---

### POST /api/v1/cluster/nodes

Add a node to the cluster.

**Request Body**
```json
{
  "node_id": "node-4",
  "addr": "127.0.0.1:8084"
}
```

**Response** `201 Created`
```json
{
  "message": "node added"
}
```

---

## Internal Endpoints (Node-to-Node)

These are used for cluster communication. Not intended for client use.

### POST /internal/v1/heartbeat

Receive a heartbeat from another node.

**Request Body**
```json
{
  "node_id": "node-2",
  "addr": "127.0.0.1:8082"
}
```

### GET/POST/DELETE /internal/v1/replicate

Replicate a read/write/delete operation.

**Query Parameters**
- `table` — table name
- `key` — item key
- `op` — `get`, `put`, or `delete`

For `put`: request body is the JSON document.

### GET /internal/v1/anti-entropy

Get key hashes for a table (used for Merkle tree comparison).

**Query Parameters**
- `table` — table name

**Response** `200 OK`
```json
{
  "keys": {
    "alice": "a1b2c3...",
    "bob": "d4e5f6..."
  }
}
```

---

## Error Format

All errors return:
```json
{
  "error": "description of what went wrong"
}
```

With an appropriate HTTP status code (400, 404, 405, 409, 500).

---

## CORS

All responses include:
```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
Access-Control-Allow-Headers: Content-Type, Authorization
```

OPTIONS requests return `204 No Content`.
