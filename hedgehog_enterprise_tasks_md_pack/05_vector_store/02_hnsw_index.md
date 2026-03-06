# Task: HNSW ANN Index + Tunables

## Purpose
HNSW provides fast approximate nearest neighbor search with strong recall and good support for dynamic inserts. This task implements HNSW for HedgehogDB vector collections, including tunables and persistence strategy, and integrates it into the query path.

## Scope
- HNSW index per collection/index.
- Parameters: `M`, `efConstruction`, `efSearch`.
- Index maintenance: inserts, deletes/updates handling (document exact semantics).
- Persistence:
  - simplest acceptable approach: rebuild on restart OR checkpoint snapshots
  - must be clearly documented
- Metrics: search latency, candidates examined, index size.

## Must-have
- Query correctness constraints: filtered results must satisfy filters.
- ANN quality guard: recall@10 threshold on synthetic set.

## Must-not
- Do not break exact search mode; keep both available.

## Subagent steps
1. **Index Engineer**: Implement HNSW core and search.
2. **Persistence Engineer**: Implement chosen persistence strategy + docs.
3. **Perf Engineer**: Add tuning knobs and performance metrics.

## Test criteria
- Integration: build index, run queries, compute recall@10 >= threshold (AT6).
- Update/delete tests in sync/async indexing mode (if implemented).

## Completion criteria
- HNSW operational and measurable, with clear tuning guidance.
