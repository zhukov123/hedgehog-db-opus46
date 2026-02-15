# Metrics and Observability Plan

HedgehogDB will emit Prometheus metrics and request trace IDs. You view metrics and (optionally) traces in Grafana, with Prometheus as the primary data source.

---

## 1. What We Emit

### 1.1 Metrics (Prometheus)

All metrics use the prefix `hedgehog_` and are designed for Prometheus scraping.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hedgehog_http_requests_total` | Counter | `method`, `operation`, `status`, `table`* | Total HTTP requests. |
| `hedgehog_http_request_duration_seconds` | Histogram | `method`, `operation`, `status`, `table`* | Request latency in seconds. |
| `hedgehog_item_operations_total` | Counter | `operation`, `result`, `table`* | Item ops: get, put, delete (success/failure). |

**Operation** is derived from the route:

- `list_tables`, `create_table`, `delete_table`
- `get_item`, `put_item`, `delete_item`, `scan_items`, `table_count`

**Status** is the HTTP status class: `2xx`, `4xx`, `5xx` (and optionally `200`, `404`, `500` for finer dashboards).

**Table** is the table name for item/table routes; `""` for global routes (e.g. list_tables).

**Latency buckets** (seconds): `0.001`, `0.005`, `0.01`, `0.025`, `0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10`.

### 1.2 Trace IDs (for correlation)

- **Request ID (trace ID):** Generated or propagated per request (e.g. `X-Request-ID` or `X-Trace-ID`).
- **Where:** Response header `X-Request-ID`; and in log lines for that request.
- **Purpose:** Correlate logs and (if you add tracing later) spans. Prometheus does not store traces; you see “traces” as correlated logs and, if you add Tempo, as trace views in Grafana.

### 1.3 Distributed tracing (OpenTelemetry + Grafana Tempo)

HedgehogDB now includes full distributed tracing via OpenTelemetry:

- **Tracer provider** initialised in `cmd/hedgehogdb/main.go` — sends spans to an OTLP HTTP endpoint (default `localhost:4318`, configurable via `OTEL_EXPORTER_OTLP_ENDPOINT`).
- **TracingMiddleware** in `internal/api/middleware.go` — creates an `http.request` span for every API call, extracts/injects W3C Trace Context headers, and sets `X-Trace-ID` on responses.
- **Coordinator child spans** — `coordinator.route_get`, `coordinator.route_put`, `coordinator.route_delete` with table/key/consistency attributes.
- **Replicate handler span** — `replicate` span on the receiving node, linked to the same trace via propagated headers.
- **Grafana Tempo** runs in Docker (see `monitoring/docker-compose.yml`) and stores trace data; add Tempo as a data source in Grafana (`http://tempo:3200`) to search and view trace timelines.

### 1.4 Replication metrics

All replication metrics use the prefix `hedgehog_replication_`. Instrumentation points: [internal/cluster/replication.go](internal/cluster/replication.go) (Replicator, HintedHandoff) and the internal replicate HTTP handler in [internal/cluster/coordinator.go](internal/cluster/coordinator.go) (lines 376–436).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hedgehog_replication_send_total` | Counter | `op` (put, delete), `target_node`, `result` (success, failure, node_down) | Outgoing replicate attempts: success, HTTP/network failure (hint stored), or node not alive (hint stored). |
| `hedgehog_replication_hinted_handoff_pending` | Gauge | `target_node` | Current number of pending hints per target node (backlog). |
| `hedgehog_replication_hint_replay_total` | Counter | `target_node`, `result` (success, failure) | Hint-replay runs (one per ReplayHints call); whether run completed or re-queued hints. |
| `hedgehog_replication_hint_replay_ops_total` | Counter | `target_node`, `op` (put, delete) | Total hint operations replayed (each put/delete during replay). |
| `hedgehog_replication_received_total` | Counter | `op` (get, put, delete), `result` (success, failure) | Incoming replicate requests handled by this node (internal handler). |
| `hedgehog_replication_received_duration_seconds` | Histogram | `op`, `result` | Latency of handling an incoming replicate request (same buckets as HTTP API). |

**Send result semantics:** `success` = HTTP 2xx; `failure` = network error or non-2xx (hint stored); `node_down` = node not alive (hint stored without sending). Pending gauge: update after every AddHint, DrainHints, and when replay re-adds a hint; pass `map[string]int` from `Handoff().PendingCounts()` to `internal/metrics` so the metrics package stays dependency-free.

---

## 2. Implementation Steps

### Phase A: Add Prometheus client and metrics

1. **Dependency**
   - Add `github.com/prometheus/client_golang` (use a recent stable version).

2. **Package `internal/metrics`**
   - Define and register Prometheus metrics:
     - **HTTP:** `hedgehog_http_requests_total` (CounterVec: method, operation, status, table), `hedgehog_http_request_duration_seconds` (HistogramVec: same labels; buckets above). Optionally `hedgehog_item_operations_total` (CounterVec: operation, result, table).
     - **Replication:** `hedgehog_replication_send_total` (CounterVec: op, target_node, result), `hedgehog_replication_hinted_handoff_pending` (GaugeVec: target_node), `hedgehog_replication_hint_replay_total` (CounterVec: target_node, result), `hedgehog_replication_hint_replay_ops_total` (CounterVec: target_node, op), `hedgehog_replication_received_total` (CounterVec: op, result), `hedgehog_replication_received_duration_seconds` (HistogramVec: op, result; same buckets). Helper: `UpdateHintedHandoffGauges(counts map[string]int)` to set pending gauges from Handoff counts (caller passes `handoff.PendingCounts()` so metrics has no cluster dependency).
   - Provide a single place to get the default `prometheus.Registry` (or use `prometheus.DefaultRegisterer`).

3. **Metrics middleware (API layer)**
   - In `internal/api`, add **MetricsMiddleware** that runs after recovery/CORS and:
     - Computes **operation** from `r.Method` and `r.URL.Path` (e.g. PUT + `/api/v1/tables/{name}/items/{key}` → `put_item`; GET same path → `get_item`; table name from path).
     - Wraps the response writer to capture **status code**.
     - Records:
       - `hedgehog_http_requests_total` with labels (method, operation, status_class, table).
       - `hedgehog_http_request_duration_seconds` with same labels and `Observe(time.Since(start).Seconds())`.
   - Apply this middleware in `internal/api/server.go` (e.g. after Recovery, before Logging).

4. **`/metrics` endpoint**
   - Register `GET /metrics` (or `GET /api/v1/metrics`) that writes the Prometheus registry in text format (`promhttp.Handler()`).
   - Register it in `server.go` or in `cmd/hedgehogdb/main.go` via `Router()` so it’s exposed on the same server as the API.

5. **Trace ID middleware**
   - Add **TraceIDMiddleware** (or extend logging middleware):
     - Read `X-Request-ID` or `X-Trace-ID` from request; if missing, generate a UUID.
     - Set the same value on the response header `X-Request-ID`.
     - Put the trace ID in `context.Context` and use it in **LoggingMiddleware** so every log line for that request includes the trace ID (e.g. `[trace_id=abc]` or a structured field).

6. **Replication metrics**
   - **Outgoing ([internal/cluster/replication.go](internal/cluster/replication.go)):** In ReplicateWrite (lines 115–155): for each replica node, increment `hedgehog_replication_send_total{op="put", target_node=nodeID, result=success|failure|node_down}`; after any AddHint, call `UpdateHintedHandoffGauges(handoff.PendingCounts())`. Same pattern in ReplicateDelete (lines 158–195) with `op="delete"`. In ReplayHints (lines 198–236): after DrainHints call `UpdateHintedHandoffGauges`; for each replayed op increment `hedgehog_replication_hint_replay_ops_total`; at end increment `hedgehog_replication_hint_replay_total{target_node, result=success|failure}`; if replay re-adds a hint, call `UpdateHintedHandoffGauges` again.
   - **Incoming ([internal/cluster/coordinator.go](internal/cluster/coordinator.go)):** In the replicate handler (lines 377–432): wrap the response writer to capture status; at start record `time.Now()`; defer a function that increments `hedgehog_replication_received_total{op, result}` and observes duration in `hedgehog_replication_received_duration_seconds` (result = success if status < 400, else failure).

### Phase B: Docker and scraping

7. **Prometheus config**
   - Create `monitoring/prometheus.yml` (or `deploy/prometheus.yml`) with a `scrape_config` for HedgehogDB:
     - Targets: `host.docker.internal:8081`, `host.docker.internal:8082`, `host.docker.internal:8083` (or the actual host/ports).
     - `metrics_path: /metrics`.
     - Optional: add `instance` or `node_id` via relabeling if you have a way to identify the node (e.g. from a label or another endpoint).

8. **Docker Compose**
   - Create `monitoring/docker-compose.yml` (or `deploy/docker-compose.monitoring.yml`) with:
     - **prometheus:** image `prom/prometheus`, mount the config, expose `9090`.
     - **grafana:** image `grafana/grafana`, expose `3000`, set admin password, optional `GF_*` for default Prometheus data source.
   - Compose file should point Prometheus at the config file (e.g. volume `./prometheus.yml:/etc/prometheus/prometheus.yml`).

### Phase C: Grafana

9. **Data source**
   - In Grafana, add a data source of type **Prometheus**, URL `http://prometheus:9090` (when running in Docker on the same network).

10. **Dashboards**
   - Create (or export) dashboards that use Prometheus queries, for example:
     - **Request rate:** `rate(hedgehog_http_requests_total[5m])` by operation, table, status.
     - **Error rate:** `rate(hedgehog_http_requests_total{status=~"4xx|5xx"}[5m]) / rate(hedgehog_http_requests_total[5m])`.
     - **Latency:** `histogram_quantile(0.95, rate(hedgehog_http_request_duration_seconds_bucket[5m]))` by operation (and optionally by table).
     - **Reads vs writes:** separate panels for `get_item`, `put_item`, `delete_item` from `hedgehog_http_requests_total` or `hedgehog_item_operations_total`.
     - **Replication:** send rate `rate(hedgehog_replication_send_total[5m])` by op and result; hinted handoff pending `hedgehog_replication_hinted_handoff_pending` by target_node; hint replay rate and failures; incoming replicate rate and latency `rate(hedgehog_replication_received_total[5m])`, `histogram_quantile(0.95, rate(hedgehog_replication_received_duration_seconds_bucket[5m]))`.
   - Optionally add **variables** for node (instance) and table so you can filter.

11. **Trace ID in logs**
    - If logs are collected (e.g. Loki), use the trace ID from the log line to correlate with metrics (e.g. find high-latency trace IDs from logs and cross-check with latency metrics).

---

## 3. File and Code Touch Points

| Area | Files / changes |
|------|------------------|
| Dependencies | `go.mod`, `go.sum` (add Prometheus client). |
| Metric definitions | New: `internal/metrics/metrics.go` (HTTP + replication counters, histograms, gauge vec, registry; `UpdateHintedHandoffGauges(map[string]int)`). |
| Middleware | `internal/api/middleware.go` (MetricsMiddleware, TraceIDMiddleware) or new `internal/api/metrics.go`. |
| Router | `internal/api/server.go`: register `GET /metrics`, apply MetricsMiddleware and TraceIDMiddleware. |
| Logging | `internal/api/middleware.go`: LoggingMiddleware logs trace ID from context. |
| Replication | [internal/cluster/replication.go](internal/cluster/replication.go): ReplicateWrite, ReplicateDelete, ReplayHints — increment send/replay counters, call `UpdateHintedHandoffGauges` on hint changes. |
| Replicate handler | [internal/cluster/coordinator.go](internal/cluster/coordinator.go): wrap response writer in replicateHandler; record replication_received_total and replication_received_duration_seconds by op and result. |
| Config | New: `monitoring/prometheus.yml`. |
| Docker | New: `monitoring/docker-compose.yml` (Prometheus + Grafana). |
| Docs | This file; optionally add a short “Observability” section to `docs/development.md` (how to run Prometheus/Grafana, where to find /metrics). |

---

## 4. Prometheus Scrape Config (example)

```yaml
# monitoring/prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: hedgehogdb
    static_configs:
      - targets:
          - host.docker.internal:8081
          - host.docker.internal:8082
          - host.docker.internal:8083
    metrics_path: /metrics
```

---

## 5. Grafana Dashboard Ideas

- **Overview:** Total request rate, error rate (%), p50/p95/p99 latency by operation.
- **By operation:** get_item, put_item, delete_item, scan_items, table_count — request count and latency.
- **By table:** request rate and latency per table (if you use the `table` label).
- **By status:** 2xx vs 4xx vs 5xx over time.
- **Per node:** If you add an `instance` or `node_id` label, one panel per node or a dropdown.
- **Replication:** Send rate by op and result; hinted handoff pending by target node; hint replay rate and failures; incoming replicate rate and latency.

---

## 6. Traces in Grafana (Tempo)

Full distributed tracing is now implemented via OpenTelemetry and Grafana Tempo. See section 1.3 for details.

---

## 7. Summary

| Deliverable | Description |
|-------------|-------------|
| **Metrics** | Counters and histograms for reads/writes (get/put/delete), success/failure (via status), latency per operation; optional item-level success/failure. **Replication:** send total (op, target_node, result), hinted handoff pending gauge, hint replay total and ops, received total and duration. |
| **Traces** | Request IDs (trace IDs) on every request and in logs; full distributed tracing via OpenTelemetry spans sent to Grafana Tempo. |
| **Where to view** | Grafana with Prometheus data source (metrics) and Tempo data source (traces); dashboards for rate, error rate, latency by operation and table, plus replication send/receive and backlog; Explore view for trace timelines. |
| **Run stack** | Docker Compose in `monitoring/` with Prometheus, Grafana, and Tempo. |
