# Task: CLI Commands for Membership + Jobs

## Purpose
This task makes the management plane usable without the UI by implementing the operational commands that map directly to Admin API resources and jobs. The focus is on membership operations and job visibility/control.

## Scope
Commands:
- `status`
- `nodes list|describe|add|decommission|remove`
- `jobs list|describe|tail`
- `rebalance start|pause|resume`
- `repair start ...`
- `snapshot create|list`
- `diagnostics bundle`

## Must-have
- `--dry-run` support where applicable (rebalance/decommission planning).
- `--wait` to block until job completes.
- Explicit `--force` and `--yes` for destructive ops.

## Must-not
- Do not hide failure reasons; print job failure details.

## Subagent steps
1. **Command Implementer**: Map commands to Admin API calls.
2. **UX/Safety**: Confirmations, warnings, and progress display.
3. **Tester**: Add integration tests that run commands against a local cluster.

## Test criteria
- E2E: start cluster, run CLI commands, assert correct state transitions.

## Completion criteria
- Operators can manage cluster lifecycle from CLI.
