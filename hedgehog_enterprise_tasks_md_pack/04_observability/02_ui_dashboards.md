# Task: Built-in UI Dashboards (Overview, Nodes, Jobs)

## Purpose
Built-in dashboards provide immediate triage without requiring external tooling. This task adds UI pages that visualize cluster health, node health, and job progress using the Admin API and metrics endpoints. The UI should be simple, fast, and opinionated.

## Scope
Pages:
- Overview: health tiles + key charts (QPS, p99, errors, under-replicated)
- Nodes: sortable table, per-node quick charts/sparklines
- Jobs: job list, progress, event logs, pause/resume controls

## Must-have
- Clear “what’s wrong” indicators and links to details.
- Job progress with phase breakdown and latest events.

## Must-not
- Do not require Grafana to view basic charts.

## Subagent steps
1. **UI Engineer**: Implement pages and data fetchers.
2. **API Integrator**: Ensure endpoints provide required data efficiently.
3. **UX Reviewer**: Ensure pages answer operator questions quickly.

## Test criteria
- UI smoke tests (render routes).
- Integration test with local cluster data.

## Completion criteria
- UI provides meaningful operational visibility by default.
