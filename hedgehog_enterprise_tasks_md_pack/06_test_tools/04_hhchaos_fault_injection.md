# Task: `hhchaos` Fault Injection Scenarios

## Purpose
Distributed DBs fail in weird ways: process crashes, network partitions, latency spikes. `hhchaos` provides controlled fault injection so the system’s recovery paths (repair, rebalance, WAL replay) can be tested automatically. It integrates with `hhbench` phases so you can run “steady -> failure -> recovery” workflows consistently.

## Scope
Scenarios:
- kill -9 node
- pause/resume node
- partition nodeA<->nodeB
- latency/jitter injection (where possible)
- disk throttle (optional if feasible)

Integration:
- Trigger faults at phase boundaries or at T+N seconds.
- Emit fault timeline into run artifacts.

## Must-have
- Works locally with docker-compose cluster, or documented constraints.
- Clean teardown.

## Test criteria
- E2E: run hhbench with hhchaos scenario and confirm recovery + hhverify pass.

## Completion criteria
- Fault scenarios reliably inject and recover in automated tests.
