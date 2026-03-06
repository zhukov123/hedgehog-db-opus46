# Task: Job System (Async Ops Framework)

## Purpose
Enterprise operations (rebalance, repair, snapshots, index builds) are not single RPCs—they are workflows. A job system makes those workflows resumable, observable, and safe. This task builds the foundation: job persistence, state transitions, progress reporting, locks, and cancellation.

## Scope
- Job model: `Pending -> Running -> (Paused) -> Succeeded|Failed|Canceled`
- Job persistence in an internal metadata store
- Job event log stream per job
- Locks to prevent conflicting jobs
- Admin API endpoints:
  - `POST /v1/jobs`
  - `GET /v1/jobs/{id}`
  - `GET /v1/jobs`
  - `POST /v1/jobs/{id}/cancel`
  - `POST /v1/jobs/{id}/pause|resume` (optional if supported immediately)

## Must-have
- Idempotency key support for `POST /v1/jobs`.
- Resumption after manager restart.
- Clear error reporting and failure states.

## Must-not
- Do not run dangerous work without persisting job state first.
- Do not allow two membership-changing jobs concurrently.

## Subagent steps
1. **Core Engineer**: Implement job persistence and transitions.
2. **Ops Engineer**: Implement locks, idempotency, and backoff strategy.
3. **Observability Engineer**: Add job metrics and structured logs.

## Test criteria
- Unit tests for state machine, idempotency, and locks.
- Integration test: create job, simulate restart, confirm job resumes/finishes.

## Completion criteria
- Job system is usable by later tasks (rebalance/repair/snapshot/index build).
