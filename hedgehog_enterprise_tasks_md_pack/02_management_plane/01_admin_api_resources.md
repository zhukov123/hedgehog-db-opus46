# Task: Admin API — Resource Endpoints

## Purpose
The Admin API is the backbone of the management plane. It provides a stable, versioned contract that both the UI and CLI rely on. This task focuses on *read-only* and *simple write* resource endpoints so other tasks can build against them early, while heavier workflow operations land later as jobs.

## Scope
Implement `v1` endpoints for:
- Cluster info
- Nodes list/describe
- Ring/ownership view
- Shards/partitions view (or your system’s equivalent)
- Health summary
- Configuration (read-only initially)

## Must-have
- Versioned routes (`/v1/...`).
- Stable JSON schemas with explicit fields.
- Backwards-compatible evolution strategy documented.

## Must-not
- No long-running operations in these endpoints (those are jobs).

## Subagent steps
1. **API Implementer**: Add endpoints and data models.
2. **Schema Reviewer**: Ensure fields are stable and well-named.
3. **Doc Writer**: Update `docs/management-plane.md` and OpenAPI (optional).

## Test criteria
- Unit tests for serialization and basic behavior.
- Integration test: start cluster, hit endpoints, assert non-empty sane responses.

## Success criteria
- CLI/UI can render cluster and node status using these endpoints.

## Completion criteria
- Endpoints implemented + documented + tested.
