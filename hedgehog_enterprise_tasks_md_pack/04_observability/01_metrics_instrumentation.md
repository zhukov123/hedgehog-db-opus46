# Task: Metrics Instrumentation + `/metrics` Endpoints

## Purpose
Metrics are the core “day-2” feature that makes the system operable. This task adds a Prometheus-compatible `/metrics` endpoint and instruments the critical paths: requests, replication, storage engine, and background jobs, using safe naming and low cardinality.

## Scope
- Add `/metrics` on each node.
- Instrument:
  - request QPS + latency histograms
  - error/timeout counters
  - replication lag/stream health
  - WAL fsync latency and queue depths
  - job progress metrics
- Add a metric naming guide in docs.

## Must-have
- Histograms for latency (bucketed).
- Minimal, bounded labels (`op`, `status`, `node_id`, `zone`).

## Must-not
- No per-key metrics.
- No unbounded shard labels outside top-N/debug.

## Subagent steps
1. **Metrics Engineer**: Implement instrumentation and endpoint.
2. **Reviewer**: Validate cardinality and units.
3. **Doc Writer**: Document metric catalog.

## Test criteria
- Integration test: `/metrics` returns expected families and labels.

## Completion criteria
- Observability foundation ready for UI dashboards and Grafana linking.
