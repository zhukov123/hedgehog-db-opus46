# Task: Workload Schema + `hhgen` Deterministic Generator

## Purpose
A serious DB needs a repeatable way to generate workloads and datasets. This task defines a JSON workload schema and implements `hhgen` to produce deterministic workloads and synthetic data across KV and vector modes, with controllable distributions and phases.

## Scope
- Workload JSON schema:
  - phases (warmup/steady/failure/recovery)
  - op mixes (read/write/delete/search)
  - distributions (uniform/Zipf)
  - sizes (values, vectors)
  - consistency and indexing mode selection
  - filters generation rules
  - seed and reproducibility metadata
- Implement `hhgen` that outputs workload specs and optionally materialized datasets.

## Must-have
- Seeded determinism.
- Schema versioning.
- Human-readable docs and examples.

## Test criteria
- Unit tests: same seed produces same output.
- Schema validation tests.

## Completion criteria
- Workload spec exists, tool generates workloads reproducibly.
