# HedgehogDB Documentation

HedgehogDB is a DynamoDB-like distributed NoSQL database built from scratch in Go with a React web UI. It uses a custom disk-based B+ tree storage engine, consistent hashing for distribution, and quorum-based replication for configurable consistency.

## Documentation Index

| Document | Description |
|----------|-------------|
| [Architecture Overview](./architecture.md) | High-level system design, component relationships, data flow |
| [Storage Engine](./storage-engine.md) | B+ tree internals, page layout, buffer pool, WAL, freelist |
| [API Reference](./api-reference.md) | All REST endpoints with request/response examples |
| [Cluster & Distribution](./cluster.md) | Consistent hashing, replication, membership, anti-entropy |
| [Web UI](./web-ui.md) | React app structure, pages, API client |
| [Development Guide](./development.md) | Building, testing, debugging, extending the codebase |
| [File Map](./file-map.md) | Every file in the project with purpose and key types/functions |

## Quick Orientation

```
hedgehog-db-opus46/
├── cmd/hedgehogdb/       # Server binary entry point
├── cmd/hedgehogctl/      # CLI tool entry point
├── internal/
│   ├── storage/          # B+ tree engine (page, pager, btree, bufferpool, wal, freelist)
│   ├── table/            # Table abstraction, catalog, table manager
│   ├── api/              # HTTP server, trie-based router, handlers, middleware
│   ├── cluster/          # Ring, membership, coordinator, replication, anti-entropy
│   ├── consistency/      # Vector clocks, quorum config
│   └── config/           # Server configuration
├── web/                  # React + Vite + TypeScript + Tailwind
├── test/                 # Integration tests
├── scripts/              # Cluster startup, data seeding
├── webui.go              # go:embed for web UI assets
├── Makefile              # Build, test, run commands
└── go.mod
```

## Key Design Decisions

1. **No external dependencies** for the database engine. Only the Go standard library.
2. **Page-oriented storage** with 4096-byte pages matching OS page size.
3. **Cell pointer arrays** within pages for O(log n) binary search without deserializing all cells.
4. **Buffer pool with LRU eviction** and pin counting to bound memory usage.
5. **WAL with after-image logging** for crash recovery. Only committed transactions are replayed.
6. **Consistent hashing with 128 virtual nodes** per physical node for balanced key distribution.
7. **Configurable quorum** (N/R/W) — strong consistency when R+W>N, eventual otherwise.
8. **Custom trie-based HTTP router** — no gorilla/mux or chi dependency.
