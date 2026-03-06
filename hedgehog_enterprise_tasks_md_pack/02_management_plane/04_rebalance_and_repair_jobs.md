# Task: Rebalance + Repair Jobs

## Purpose
Rebalancing and repair are the long-running maintenance operations that keep distributed NoSQL reliable over time. This task implements these operations as controllable jobs with throttles, pause/resume, and safety checks, enabling operators to run them during business hours without saturating the cluster.

## Scope
- `rebalance.start|pause|resume`: compute plan, move ownership/data, validate, finish
- `repair.full|shard|range`: run anti-entropy, verify convergence, report

## Must-have
- Throttles: max concurrency, max MB/s (or equivalent)
- Pause/resume support for rebalance (repair optional)
- Progress metrics: bytes moved, ranges repaired, ETA estimate if feasible

## Must-not
- Do not violate RF or availability during movement.
- Do not run repair that overloads nodes without throttle options.

## Subagent steps
1. **Rebalance Engineer**: Plan computation + execution engine.
2. **Repair Engineer**: Orchestrate anti-entropy runs and convergence checks.
3. **UI/CLI Integrator**: Ensure both operations are visible and controllable.

## Test criteria
- E2E: start rebalance, pause, resume, completion; repair convergence (AT3).

## Completion criteria
- Rebalance/repair jobs are reliable, observable, and safe.
