# Task: Release Checklist + Operator Docs

## Purpose
A “release” for an enterprise-ish DB feature set is not just code—it’s operator docs, runbooks, and sanity checks. This task ensures all docs exist, that UI/CLI workflows are documented, and that default configs are safe.

## Scope
- Operator docs:
  - management plane usage
  - CLI cookbook
  - observability and Grafana linking
  - vector store usage and tuning
  - testing tools usage
- Runbooks:
  - node down
  - under-replication
  - rebalance stuck
  - repair convergence issues
  - snapshot failure

## Must-have
- Every major feature is documented with examples.
- A final “release” script that runs the acceptance tests.

## Completion criteria
- Docs and runbooks complete; acceptance tests pass on a clean checkout.
