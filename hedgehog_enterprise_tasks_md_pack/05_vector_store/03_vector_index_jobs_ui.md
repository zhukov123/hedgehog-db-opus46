# Task: Vector Index Jobs + UI Visibility

## Purpose
Indexes in real systems must be buildable, rebuildable, and observable without downtime. This task integrates vector index operations into the job framework and adds UI/CLI exposure so operators can manage index lifecycle safely.

## Scope
- Jobs: `vector.index.build`, `vector.index.rebuild`.
- Admin endpoints: list indexes, index stats.
- UI: vector page showing index health and build progress.
- CLI: commands to create/build/rebuild and view stats (if you prefer, separate CLI task).

## Must-have
- Job progress and event logs.
- Safe behavior if index build is interrupted and resumed/restarted.

## Completion criteria
- Operator can build/rebuild indexes and see progress end-to-end.
