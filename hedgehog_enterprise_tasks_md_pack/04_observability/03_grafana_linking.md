# Task: Grafana Deep-Linking from Built-in UI

## Purpose
Grafana is the power-user tool for long-range analysis and custom dashboards. This task adds optional deep-links from built-in UI panels to Grafana dashboards, preserving time range and key variables (cluster/node/collection). If Grafana isn’t configured, the UI remains fully usable.

## Scope
- Config: `grafana_base_url` + dashboard UID mapping + variable names.
- UI “Open in Grafana” per panel.
- URL builder: include `from/to` and variables.

## Must-have
- Links only appear if configured.
- Variables include at least: cluster id, node id (and vector collection when relevant).

## Must-not
- Do not leak credentials in URLs.

## Subagent steps
1. **Config Engineer**: Add config schema + validation.
2. **UI Engineer**: Add link buttons and builder logic.
3. **Tester**: Add a smoke test for correct URL composition.

## Completion criteria
- UI can jump to Grafana seamlessly when configured.
