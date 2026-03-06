# Task: CI Targets & Automation Scripts

## Purpose
To keep this project maintainable, the new feature set must be continuously validated. This task adds CI targets for unit/integration/e2e, plus scripts to bring up a local cluster for development and run the acceptance tests.

## Scope
- Add `make` targets (or repo-native equivalent):
  - `test-unit`
  - `test-e2e`
  - `test-vector`
  - `test-crash`
  - `bench-smoke`
- Add scripts:
  - `scripts/dev_cluster_up.sh`
  - `scripts/run_e2e.sh`
  - `scripts/run_vector_suite.sh`
- GitHub Actions workflow updates.

## Must-have
- CI runs in reasonable time; keep datasets small.
- Artifacts uploaded for failures (logs, oplog, summaries).

## Completion criteria
- CI reliably validates core features on every PR.
