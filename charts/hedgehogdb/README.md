# HedgehogDB Helm Chart

Deploy HedgehogDB (distributed key-value store) as a StatefulSet with ConfigMap-based app settings and optional Ingress.

## Prerequisites

- Kubernetes 1.19+
- The HedgehogDB container image must be available (build and push to a registry, or load into the cluster locally).

## Install

```bash
# Create namespace
kubectl create namespace hedgehogdb

# Install with default values (image: hedgehogdb:latest, 3 replicas, Ingress at hedgehogdb.local)
helm install hedgehogdb ./charts/hedgehogdb -n hedgehogdb

# Or override image and replicas
helm install hedgehogdb ./charts/hedgehogdb -n hedgehogdb \
  --set image.repository=myreg/hedgehogdb \
  --set image.tag=v0.1.0 \
  --set image.pullPolicy=IfNotPresent \
  --set replicaCount=5
```

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of HedgehogDB nodes | `3` |
| `image.repository` | Image repository | `hedgehogdb` |
| `image.tag` | Image tag | `latest` |
| `image.pullPolicy` | Pull policy | `Never` (for local load) |
| `config.*` | App settings (replication_n, quorums, buffer_pool_size, etc.) | See values.yaml |
| `persistence.size` | PVC size per node | `5Gi` |
| `persistence.storageClass` | Storage class (empty = default) | `""` |
| `otel.exporterOtlpEndpoint` | OTLP endpoint for tracing (e.g. Tempo) | `""` |
| `ingress.enabled` | Enable Ingress | `true` |
| `ingress.host` | Host for Ingress | `hedgehogdb.local` |
| `ingress.className` | Ingress class (e.g. traefik) | `traefik` |

`node_id`, `bind_addr`, `data_dir`, and `seed_nodes` are set per-pod by the container entrypoint (from Kubernetes Downward API). Do not set them in `config` for Helm deployments.

## Cluster formation

Pods start in order (0, 1, 2, ...). Pod 0 has no seeds; pod 1 seeds from pod 0; pod 2 seeds from 0 and 1. The headless Service provides stable DNS (`<release-name>-0.<release-name>`, ...) so each pod can reach the others. No separate init job is required.

## Uninstall

```bash
helm uninstall hedgehogdb -n hedgehogdb
# PVCs remain; delete namespace to remove them if desired.
```
