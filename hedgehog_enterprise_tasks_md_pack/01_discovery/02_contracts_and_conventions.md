# Task: Contracts & Conventions (Jobs, Epochs, Metrics, Reproducibility)

## Purpose
Enterprise features only work if the internal contracts are explicit: configuration epochs prevent stale membership changes, jobs are resumable and observable, metrics are consistent and safe, and tests are reproducible. This task defines those contracts once so every subagent builds to the same rules.

## Scope
- Define job model: states, progress, event log, idempotency keys, cancellation semantics.
- Define membership epoch/config versioning and how nodes reject stale plans.
- Define metric naming, units, and label cardinality rules.
- Define workload determinism and ledger format for test tools.

## Must-have
- Written contracts in docs.
- Minimal reference implementation stubs (interfaces/types) if helpful.

## Must-not
- Do not over-specify; keep it implementable.

## Subagent steps
1. **Spec Author**: Draft contracts (jobs/epochs/metrics/tests).
2. **API Designer**: Draft resource model for Admin API.
3. **Integrator**: Confirm contracts match codebase idioms; adjust.

## Deliverables
- `docs/plan/contracts.md`
- `docs/plan/api_overview.md`
- `docs/plan/metrics_conventions.md`
- `docs/plan/testing_conventions.md`

## Test criteria
- N/A (spec) but must be internally consistent.

## Completion criteria
- All later PRs can point to these contracts and comply.
