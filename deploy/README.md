# Deploy HedgehogDB to Kubernetes (k3s)

## Getting the image to the cluster

If **k3s runs on this machine**: build and load into k3s’s containerd:

```bash
docker build -t hedgehogdb:latest .
docker save hedgehogdb:latest | sudo k3s ctr images import -
```

If **k3s runs on another machine**, use one of:

**A. Save, copy, and import on the k3s node**

```bash
# On your machine (where you built the image)
docker save hedgehogdb:latest -o hedgehogdb.tar
scp hedgehogdb.tar user@k3s-server:/tmp/

# On the k3s server
sudo k3s ctr images import /tmp/hedgehogdb.tar
```

**B. Use a registry (no file copy)**

Push from your machine, then on the k3s node pull (or set `image.pullPolicy: IfNotPresent` and the image name in Helm/StatefulSet):

```bash
# On your machine: tag and push (e.g. Docker Hub or GHCR)
docker tag hedgehogdb:latest your-registry/hedgehogdb:latest
docker push your-registry/hedgehogdb:latest
```

Then install with Helm using the registry image: `--set image.repository=your-registry/hedgehogdb --set image.tag=latest --set image.pullPolicy=IfNotPresent`. Or update `deploy/k8s/statefulset.yaml` / chart `values.yaml` to use that image and remove `imagePullPolicy: Never`.

---

## Option A: Helm (recommended)

```bash
kubectl create namespace hedgehogdb
# Ensure image is available (see above), then:
helm install hedgehogdb ./charts/hedgehogdb -n hedgehogdb
```

Override image, replicas, or config via `--set` or a custom `values.yaml`. See [charts/hedgehogdb/README.md](../charts/hedgehogdb/README.md).

## Option B: Raw manifests

```bash
kubectl create namespace hedgehogdb
# Build and load image as above

kubectl apply -f deploy/k8s/
# Optional: monitoring
kubectl apply -f deploy/k8s/monitoring/
```

## Access

- **API / Web UI**: Port-forward `kubectl port-forward svc/hedgehogdb-api 8080:8080 -n hedgehogdb` then open http://localhost:8080
- **Ingress**: If Traefik is installed, add to `/etc/hosts`: `hedgehogdb.local` and (if monitoring) `grafana.hedgehogdb.local` pointing to the ingress IP.

## Cluster formation

Pods start in order (hedgehogdb-0, hedgehogdb-1, hedgehogdb-2). Each pod gets identity and seed nodes from the entrypoint (POD_NAME + headless service DNS). No extra init job needed. See [docs/k3s-deployment-plan.md](../docs/k3s-deployment-plan.md).
