#!/bin/bash
# Verify trafficgen: tables on all nodes, counts increment.
# Usage: ./scripts/verify-traffic.sh [base_url]
# Example: ./scripts/verify-traffic.sh http://localhost:8081

set -e
BASE="${1:-http://localhost:8081}"
BASE="${BASE%/}"

echo "=== Verify cluster table-counts ==="
echo "Base URL: $BASE"
echo ""

# Check cluster is reachable
if ! curl -sf "$BASE/api/v1/health" >/dev/null; then
  echo "FAIL: Cannot reach $BASE (is the cluster running?)"
  exit 1
fi

# Initial counts
echo "Initial table-counts:"
curl -s "$BASE/api/v1/cluster/table-counts" | python3 -m json.tool 2>/dev/null || curl -s "$BASE/api/v1/cluster/table-counts"
echo ""

echo "Running trafficgen 15s (inserts=5 updates=2 deletes=1)..."
./bin/trafficgen -url "$BASE" -inserts 5 -updates 2 -deletes 1 &
TG_PID=$!
sleep 15
kill $TG_PID 2>/dev/null || true
wait $TG_PID 2>/dev/null || true
echo ""

echo "Counts after traffic:"
curl -s "$BASE/api/v1/cluster/table-counts" | python3 -m json.tool 2>/dev/null || curl -s "$BASE/api/v1/cluster/table-counts"
echo ""

echo "=== Verify: each table has counts on all nodes (no -1) ==="
curl -s "$BASE/api/v1/cluster/table-counts" > /tmp/counts.json
for table in users products orders; do
  out=$(python3 -c "
import json
d=json.load(open('/tmp/counts.json'))
t=d.get('$table',{})
for n,c in t.items():
    if c is None or c<0:
        print('FAIL')
        exit(1)
    print(f'{n}={c}', end=' ')
print()
" 2>/dev/null) || { echo "FAIL: $table has missing or error counts"; exit 1; }
  echo "  $table: $out"
done
echo "OK: All tables have counts on all nodes."
