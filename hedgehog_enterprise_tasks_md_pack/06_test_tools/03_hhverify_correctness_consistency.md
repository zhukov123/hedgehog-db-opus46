# Task: `hhverify` Correctness + Consistency Verification

## Purpose
Verification is what turns “it seems fine” into “it’s correct.” This task implements `hhverify` to consume the operation ledger and validate correctness properties for each supported mode: no out-of-thin-air reads, read-your-writes when promised, convergence after repair, and vector filter correctness. It also provides optional offline storage verification when feasible.

## Scope
- Ledger replay checking.
- Consistency checkers:
  - monotonic reads (per client)
  - read-your-writes (per client, when expected)
  - convergence checks after a repair window
- Vector checks:
  - filter correctness
  - recall estimation via exact baseline sampling
- Optional offline checks:
  - WAL and B+tree invariant verification (best-effort)

## Must-have
- Clear, actionable failure reports (which ops violated what rule).
- Deterministic verification (no randomness without logged seed).

## Test criteria
- Unit tests for checker logic.
- Integration: run a workload + verify passes.

## Completion criteria
- `hhverify` can validate real runs and catch meaningful bugs.
