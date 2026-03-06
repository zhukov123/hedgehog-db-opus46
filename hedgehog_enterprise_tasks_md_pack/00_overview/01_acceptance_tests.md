# Acceptance Tests (AT) Catalog

## Purpose
This file defines the end-to-end acceptance tests that prove the system behaves correctly across membership changes, durability events, vector search modes, and operational tooling. The goal is to ensure each feature addition has an objective validation path and that regressions become obvious.

## AT1: Membership add + rebalance
**Scenario:** Start 3 nodes, ingest 50k keys, add a 4th node, run rebalance.  
**Assertions:** ring epoch increments; keys remain readable under quorum; ownership distribution skew decreases.

## AT2: Decommission under load
**Scenario:** 3 nodes with mixed workload, decommission node2 with drain+rebalance.  
**Assertions:** no under-replicated shards at end; cluster remains available; node transitions to DECOMMISSIONED.

## AT3: Repair convergence
**Scenario:** induce divergence (drop replication messages temporarily), run repair job.  
**Assertions:** convergence for sampled keys; repair metrics increment; job completes with success.

## AT4: Crash durability (single-node)
**Scenario:** run writes, SIGKILL at random points, restart, replay WAL.  
**Assertions:** all acknowledged writes persist (ledger-based); DB starts clean; verification passes.

## AT5: Vector exact correctness
**Scenario:** insert 10k vectors (seeded), run 200 queries, do exact topK.  
**Assertions:** exact results match brute-force baseline; filter correctness holds.

## AT6: Vector ANN quality
**Scenario:** build HNSW, run 200 queries, compare to exact baseline.  
**Assertions:** recall@10 >= threshold; filtered recall meets threshold; latency improves vs exact.

## AT7: UI/Grafana link smoke
**Scenario:** set Grafana config, open UI.  
**Assertions:** UI shows “Open in Grafana” links; links include time range and variables.

## Automation note
Each AT should be implemented as a runnable script plus a CI job. Keep CI datasets small (e.g., 10k vectors) and run longer soak tests optionally.
