# Task: Membership Workflows (Add / Decommission / Remove Nodes)

## Purpose
Adding and removing nodes safely is the defining “enterprise” feature for a distributed database. This task implements the operational workflows as jobs, including safety preflights and state transitions, so operators can scale the cluster without outages or data loss.

## Scope
Jobs:
- `node.add`: register -> join -> active
- `node.decommission`: draining -> move ownership -> validate -> decommissioned
- `node.remove`: remove from membership after decommissioned (with `--force` escape hatch)

Safety rails:
- Refuse if RF would be violated
- Refuse if too many nodes are draining
- Epoch/versioned config updates

## Must-have
- Node state machine: `NEW/JOINING/ACTIVE/DRAINING/DECOMMISSIONED/REMOVED`
- CLI/UI-visible progress and per-phase events
- Dry-run plan for decommission (what will move, how much)

## Must-not
- No silent force remove. `--force` must be explicit.

## Subagent steps
1. **Workflow Engineer**: Implement node state transitions and job logic.
2. **Planner Engineer**: Implement movement plan computation for drain.
3. **Verifier Engineer**: Add invariants and validation after each phase.

## Test criteria
- E2E: add node under load; decommission under load (AT1/AT2).

## Completion criteria
- Membership changes work reliably and leave the cluster healthy.
