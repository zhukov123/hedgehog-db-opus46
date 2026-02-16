#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MONITORING_DIR="$PROJECT_ROOT/monitoring"

echo "=== HedgehogDB Observability Stack ==="
echo ""

# Check Docker is running
if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker is not running. Please start Docker first."
  exit 1
fi

# Start the stack
echo "[1/4] Starting Prometheus, Tempo, and Grafana..."
cd "$MONITORING_DIR"
docker compose up -d 2>&1 | grep -v "^$"

# Wait for services to be healthy
echo ""
echo "[2/4] Waiting for services to be ready..."

MAX_WAIT=60
ELAPSED=0
while [ $ELAPSED -lt $MAX_WAIT ]; do
  PROM_OK=$(curl -sf http://localhost:9090/-/ready 2>/dev/null && echo "yes" || echo "no")
  GRAFANA_OK=$(curl -sf http://localhost:3001/api/health 2>/dev/null && echo "yes" || echo "no")
  TEMPO_OK=$(curl -sf http://localhost:3200/ready 2>/dev/null && echo "yes" || echo "no")

  if [ "$PROM_OK" = "yes" ] && [ "$GRAFANA_OK" = "yes" ] && [ "$TEMPO_OK" = "yes" ]; then
    break
  fi

  sleep 2
  ELAPSED=$((ELAPSED + 2))
  printf "  waiting... (Prometheus=%s Grafana=%s Tempo=%s)\r" "$PROM_OK" "$GRAFANA_OK" "$TEMPO_OK"
done
echo ""

if [ $ELAPSED -ge $MAX_WAIT ]; then
  echo "WARNING: Some services may not be fully ready yet."
fi

# Verify datasources
echo "[3/4] Verifying datasources..."
DS_COUNT=$(curl -sf -u admin:admin http://localhost:3001/api/datasources 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
echo "  Datasources configured: $DS_COUNT"

# Verify dashboard
echo "[4/4] Verifying dashboard..."
DASH=$(curl -sf -u admin:admin "http://localhost:3001/api/dashboards/uid/hedgehogdb-overview" 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['dashboard']['title'])" 2>/dev/null || echo "")
if [ -n "$DASH" ]; then
  echo "  Dashboard: $DASH"
else
  echo "  Dashboard: loading (may take a few seconds for provisioning)..."
fi

echo ""
echo "=== Observability Stack Ready ==="
echo ""
echo "  Grafana:    http://localhost:3001  (admin/admin)"
echo "  Dashboard:  http://localhost:3001/d/hedgehogdb-overview"
echo "  Prometheus: http://localhost:9090"
echo "  Tempo:      http://localhost:3200"
echo ""
echo "  HedgehogDB nodes export traces to localhost:4318 (OTLP HTTP)"
echo "  HedgehogDB nodes export metrics on /metrics (scraped by Prometheus)"
echo ""
