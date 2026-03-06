# HedgehogDB k3s Deployment Plan

This document outlines a step-by-step plan to deploy HedgehogDB (and optional monitoring) to a k3s cluster.

---

## 1. Overview

| Component | Description |
|-----------|-------------|
| **HedgehogDB** | Go binary; each node needs `-node-id`, `-bind`, `-data-dir`, `-seed-nodes`. Serves HTTP API + embedded Web UI on one port (default 8080). |
| **Discovery** | Nodes find each other via seed nodes. In k3s we use a **StatefulSet** so pod DNS is stable: `hedgehogdb-0.hedgehogdb`, `hedgehogdb-1.hedgehogdb`, ... |
| **Data** | Each node needs persistent storage for `data_dir` (B+ tree + WAL). Use a **PersistentVolumeClaim** per pod. |
| **Monitoring** (optional) | Prometheus, Grafana, Tempo as in `monitoring/docker-compose.yml`; can be run as separate k8s Deployments or skipped if you use cluster-wide monitoring. |

---

## 2. Prerequisites

- [ ] **k3s cluster** up and `kubectl` configured (`kubectl get nodes` works).
- [ ] **Container image** for HedgehogDB:
  - Either build and push to a registry (e.g. GHCR, Docker Hub, private registry), or
  - Use k3s’s local image loading (build on each node or use `k3s ctr images import` after `docker save`).
- [ ] **Namespace** (e.g. `hedgehogdb`) for the app.

```bash
kubectl create namespace hedgehogdb
```

---

## 3. Step 1: Containerize HedgehogDB

Add a **Dockerfile** at the repo root that builds the single binary and runs it.

**Suggested layout:**

- **Multi-stage build**: stage 1 build Go binary, stage 2 minimal runtime image (e.g. `scratch` or `alpine`).
- **Default bind**: `0.0.0.0:8080` (already the app default).
- **Default data dir**: e.g. `/data` inside the container.
- **Entrypoint**: run `hedgehogdb` with flags passed via args or env (see Step 4).

Example (to be created):

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /hedgehogdb ./cmd/hedgehogdb

# Runtime stage
FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /hedgehogdb /hedgehogdb
EXPOSE 8080
ENTRYPOINT ["/hedgehogdb"]
# Defaults; overridden by StatefulSet args
CMD ["-bind", "0.0.0.0:8080", "-data-dir", "/data"]
```

- Build: `docker build -t <your-registry>/hedgehogdb:latest .`
- Push: `docker push <your-registry>/hedgehogdb:latest` (or load into k3s as above).

---

## 4. Step 2: Kubernetes Manifests (HedgehogDB cluster)

Create a directory, e.g. **`deploy/k8s/`**, with the following.

### 4.1 Headless Service (for StatefulSet DNS)

- **Name**: e.g. `hedgehogdb`.
- **Purpose**: Gives each pod a stable DNS name: `hedgehogdb-0.hedgehogdb.hedgehogdb.svc.cluster.local` (and :8080).
- **Port**: 8080 (app port).

### 4.2 StatefulSet

- **Replicas**: e.g. 3 (or 5 for larger clusters; match with replication/quorum in config).
- **ServiceName**: same as headless service name (`hedgehogdb`).
- **Pod template**:
  - **Image**: your HedgehogDB image.
  - **Port**: containerPort 8080.
  - **Args** (per pod):
    - `-node-id` → from pod name (e.g. `node-0`, `node-1`, …) or fixed mapping.
    - `-bind` → `0.0.0.0:8080`.
    - `-data-dir` → `/data`.
    - `-seed-nodes` → only for **index > 0**; comma-separated list of previous pods, e.g. `hedgehogdb-0.hedgehogdb:8080`, or `hedgehogdb-0.hedgehogdb:8080,hedgehogdb-1.hedgehogdb:8080` for pod 2. Use **Downward API** or a small init container to build this list from `POD_NAME` and `NAMESPACE`.
  - **VolumeMounts**: mount a volume at `/data` (from `volumeClaimTemplates`).
- **volumeClaimTemplates**:
  - One PVC per pod (e.g. `data`), size e.g. 5Gi–10Gi, storageClassName as per your k3s (often `local-path` or default).

**Seed nodes in k8s:**  
Pod `hedgehogdb-0`: no seed nodes.  
Pod `hedgehogdb-1`: `-seed-nodes hedgehogdb-0.hedgehogdb:8080`.  
Pod `hedgehogdb-2`: `-seed-nodes hedgehogdb-0.hedgehogdb:8080,hedgehogdb-1.hedgehogdb:8080`.  
Pattern: all previous pods’ addresses. You can generate this in the container entrypoint using `POD_NAME` and `NAMESPACE`, or with an init container that writes args to a shared place.

### 4.3 Service (API / Web UI access)

- **Name**: e.g. `hedgehogdb-api`.
- **Type**: ClusterIP (default) or LoadBalancer/NodePort if you need external access.
- **Selector**: same as StatefulSet (e.g. `app: hedgehogdb`).
- **Port**: 8080 → targetPort 8080.

Clients (and Grafana/Prometheus if in-cluster) use `hedgehogdb-api.hedgehogdb.svc.cluster.local:8080` for load-balanced access to any node.

### 4.4 Optional: ConfigMap for JSON config

If you prefer a config file instead of flags, add a ConfigMap with a JSON config and mount it; run with `-config /etc/hedgehogdb/config.json`. Seed nodes in that config can still use the same DNS names (e.g. `hedgehogdb-0.hedgehogdb:8080`).

---

## 5. Step 3: Bootstrap order and readiness

- **StatefulSet** starts pods in order (0, 1, 2, …). That matches “first node has no seeds, others seed from previous nodes.”
- Add a **readinessProbe** (e.g. `httpGet http://:8080/api/v1/cluster/status` or `/health` if you add it) so Services don’t send traffic to pods that aren’t ready.
- Optionally add **livenessProbe** on the same or a light endpoint to restart stuck nodes.

---

## 6. Step 4: OpenTelemetry (optional)

The app uses `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4318`). To enable tracing in k8s:

- Deploy an OTLP collector or Tempo (e.g. all-in-one) in the cluster.
- Set **env** in the StatefulSet: `OTEL_EXPORTER_OTLP_ENDPOINT: http://<tempo-or-collector-service>:4318`.

If you don’t set it, the app logs a warning and continues without tracing.

---

## 7. Step 5: Monitoring (optional)

- **Prometheus**: Deploy Prometheus (e.g. Helm or a simple Deployment + ConfigMap) and add a scrape config for HedgehogDB:
  - **Static config**: list `hedgehogdb-0.hedgehogdb:8080`, `hedgehogdb-1.hedgehogdb:8080`, … under the headless service, or
  - **Service discovery**: if you use pod discovery, use the same label selector as the StatefulSet and scrape `:8080/metrics`.
- **Grafana**: Deploy Grafana, add Prometheus (and Tempo) as datasources, import the dashboard from `monitoring/grafana/dashboards/hedgehogdb.json`.
- **Tempo**: Deploy Tempo and point `OTEL_EXPORTER_OTLP_ENDPOINT` to it (Step 6).

You can keep using the same `monitoring/prometheus.yml` logic (targets) but with in-cluster DNS names instead of `host.docker.internal`.

---

## 8. Step 6: Storage (k3s)

- k3s often uses **local-path-provisioner** (default StorageClass). Your PVCs will bind to PVs on the node where the pod runs.
- For a **single-node** k3s cluster, all PVCs live on that node; ensure enough disk.
- For **multi-node**, consider whether you want to pin HedgehogDB pods to specific nodes (e.g. for larger local disks) using nodeSelector or affinity, and a StorageClass that matches.

---

## 9. Checklist summary

| # | Task | Notes |
|---|------|--------|
| 1 | Create `Dockerfile` | Multi-stage build for `cmd/hedgehogdb` |
| 2 | Build and push (or load) image | Registry or k3s local |
| 3 | Create `deploy/k8s/` | Headless Service, StatefulSet, API Service |
| 4 | Implement seed-nodes for StatefulSet | From POD_NAME + headless service DNS |
| 5 | Add volumeClaimTemplates + readinessProbe | PVC for `/data`, HTTP probe on :8080 |
| 6 | Deploy to namespace `hedgehogdb` | `kubectl apply -f deploy/k8s/` |
| 7 | (Optional) OTEL endpoint env | If using Tempo/collector |
| 8 | (Optional) Deploy Prometheus/Grafana/Tempo | Use in-cluster DNS for scrape and OTLP |

---

## 10. Quick test after deploy

```bash
# From inside the cluster (or port-forward)
kubectl port-forward -n hedgehogdb svc/hedgehogdb-api 8080:8080
# Then: curl http://localhost:8080/api/v1/cluster/status
# and open http://localhost:8080 for the Web UI.
```

---

## 11. Next steps (concrete files)

1. **Add `Dockerfile`** in repo root (or under `deploy/`).
2. **Add `deploy/k8s/hedgehogdb.yaml`** (or split into `service.yaml`, `statefulset.yaml`) with Headless Service, StatefulSet (with seed-nodes logic), and ClusterIP Service.
3. Optionally add **`deploy/k8s/monitoring/`** with Prometheus/Grafana/Tempo manifests or Helm values references.

If you want, the next step can be generating the actual `Dockerfile` and `deploy/k8s/*.yaml` files tailored to your image name and replica count.
