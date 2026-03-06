# Task: Snapshots (MVP) + Restore Hooks

## Purpose
Backups are non-negotiable for enterprise use. This task delivers an MVP snapshot system that can create and list snapshots and lays the groundwork for restore (even if full PITR comes later). The emphasis is correctness, operator usability, and integration with jobs/metrics/UI.

## Scope
- Snapshot create job: write snapshot to local dir (and optionally object store later)
- Snapshot list endpoint
- Snapshot metadata: time, size, cluster epoch, checksum
- Restore hooks/stubs (documented) for later expansion

## Must-have
- Snapshot creation is a job with progress and failure reporting.
- Snapshot integrity verification (checksums).

## Must-not
- Do not claim PITR unless implemented.

## Subagent steps
1. **Backup Engineer**: Implement snapshot creation + listing.
2. **Verifier Engineer**: Implement snapshot integrity checks.
3. **Docs/UI**: Add UI page and docs.

## Test criteria
- E2E: create snapshot under light load; verify it is listed and passes checksum.

## Completion criteria
- Operators can create/list snapshots and see status in UI/CLI.
