# Task: Repo Audit & Integration Points

## Purpose
Before adding enterprise features, we need a factual map of the codebase: how nodes start, how membership and replication are wired, what storage engine boundaries exist, how the current UI/admin endpoints work, and how configuration is loaded. This task prevents “design drift” and ensures that later tasks integrate cleanly instead of duplicating infrastructure.

## Scope
- Build a module map (storage, WAL, replication, membership, API, UI).
- Identify existing conventions: config schema, logging, metrics (if any), HTTP frameworks.
- Locate extension points for: Admin API, Jobs, CLI client, vector index, test harnesses.

## Must-have
- Written audit with file/dir references.
- Proposed integration plan (where code should go).
- List of risky areas and “do not touch without tests”.

## Must-not
- Do not refactor large subsystems during audit. This is informational.

## Subagent steps
1. **Code Cartographer**: Inventory modules, entrypoints, packages, build tooling.
2. **API Mapper**: Identify all existing network endpoints and their authentication story.
3. **UI Mapper**: Determine current UI framework and how it fetches data.
4. **Reviewer**: Produce a short summary + recommended coding patterns.

## Deliverables
- `docs/plan/current_architecture.md`
- `docs/plan/integration_points.md`
- `docs/plan/risks.md`

## Test criteria
- N/A (informational) but outputs must be consistent with actual code.

## Completion criteria
- All later tasks can cite concrete integration points rather than guessing.
