# Definitions of Done (DoD)

## Purpose
This doc lists completion checklists per deliverable area so that subagents implement features in a consistent, verifiable way. It’s also the rubric used by the “reviewer” subagent to accept/reject work.

## Admin/Jobs DoD
- [ ] Job persistence + resume works after restart.
- [ ] Locks prevent conflicting ops.
- [ ] Membership epoch increments and is enforced (reject stale plans).
- [ ] Dry-run rebalance plan exists.
- [ ] E2E membership change passes (AT1/AT2).

## CLI DoD
- [ ] Core command set implemented (status, nodes add/drain/remove, jobs, repair, rebalance, snapshot, diagnostics).
- [ ] JSON output stable and documented.
- [ ] Dangerous ops require confirmation or `--yes`.
- [ ] Integration tests exist.

## Observability/UI DoD
- [ ] `/metrics` on every node.
- [ ] UI overview/nodes/jobs pages show meaningful data.
- [ ] Grafana deep-link works when configured; hidden otherwise.
- [ ] Metric catalog documented.

## Vector DoD
- [ ] Exact search baseline correct.
- [ ] Filters correct (no false positives).
- [ ] HNSW build/search works with tunables.
- [ ] Index build/rebuild are jobs.
- [ ] Recall@K threshold met on synthetic dataset (AT6).
- [ ] UI shows vector index health.

## Test Tools DoD
- [ ] Workload schema defined; deterministic seed.
- [ ] Tools: generator, bench, verify, chaos.
- [ ] Crash test exists and verifies no lost acknowledged writes.
- [ ] CI targets added; core suites run in Actions.

## Global DoD
- [ ] Docs updated for every new feature.
- [ ] No uncontrolled metric cardinality.
- [ ] `make test` / `make lint` / `make e2e` pass.
