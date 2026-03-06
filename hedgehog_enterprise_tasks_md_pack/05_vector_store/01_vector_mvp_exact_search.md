# Task: Vector Store MVP (Exact Search Baseline)

## Purpose
A correct baseline is essential before adding ANN indexes. This task adds vector storage and exact (brute-force) kNN search so correctness is provable. It also establishes the API contract and metadata filtering semantics needed for RAG workloads.

## Scope
- Vector field type: float32 array with fixed dimension per collection/index.
- Collection model: `id`, `embedding`, `metadata`, optional `text`.
- CRUD: upsert, delete.
- Search: exact topK with metric (cosine/L2).
- Filters: at least post-filter correctness.

## Must-have
- Dimension mismatch rejected.
- Filter correctness (no false positives).
- Deterministic search results for identical inputs.

## Must-not
- Do not add ANN yet; keep baseline simple and provable.

## Subagent steps
1. **API/Data Engineer**: Add endpoints and storage.
2. **Search Engineer**: Implement exact search and distance metrics.
3. **Filter Engineer**: Implement filter parsing and evaluation.

## Test criteria
- Unit tests: dim mismatch, metric correctness.
- Integration: insert 10k vectors, run queries, validate against expected baseline (AT5).

## Completion criteria
- Exact vector search works reliably and is documented.
