# Task: `hhbench` Load/Perf Runner + Ledger Emission

## Purpose
`hhbench` executes workloads against a live cluster and measures latency/throughput while emitting a canonical operation ledger (`oplog.jsonl`). This ledger is later used for correctness checks and durability verification. The tool must be stable enough to run in CI as a regression guard.

## Scope
- Execute workload spec phases.
- Record per-op timing and outcomes.
- Emit `oplog.jsonl` with request/response hashes, modes, and identifiers.
- Produce summary JSON: p50/p95/p99, QPS, error rate.
- Optional: export Prometheus-style summary metrics.

## Must-have
- Deterministic run metadata (seed, versions, cluster epoch).
- Clean exit codes for failures.

## Test criteria
- Integration test against local cluster.
- Golden summary structure test.

## Completion criteria
- `hhbench` usable as perf smoke and as ledger generator.
