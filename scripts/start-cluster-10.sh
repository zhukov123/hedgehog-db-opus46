#!/bin/bash
set -e

BINARY="./bin/hedgehogdb"
N=10

if [ ! -f "$BINARY" ]; then
    echo "Building..."
    make build
fi

echo "Starting ${N}-node HedgehogDB cluster..."

# Create data directories
for i in $(seq 1 $N); do
    mkdir -p "data/node${i}"
done

# Build seed list for each node: node 1 has none, node 2 seeds 1, node 3 seeds 1,2, ...
SEEDS=""
PIDS=""

for i in $(seq 1 $N); do
    PORT=$((8080 + i))
    NODE_ID="node-${i}"
    DATA_DIR="data/node${i}"
    $BINARY \
        -node-id "$NODE_ID" \
        -bind "127.0.0.1:${PORT}" \
        -data-dir "./${DATA_DIR}" \
        ${SEEDS:+-seed-nodes "$SEEDS"} &
    PIDS="$PIDS $!"
    echo "Node ${i} started (PID $!) on :${PORT}"
    [ -z "$SEEDS" ] && SEEDS="127.0.0.1:${PORT}" || SEEDS="${SEEDS},127.0.0.1:${PORT}"
    sleep 1
done

echo ""
echo "Cluster is running!"
echo "  Nodes: http://127.0.0.1:8081 .. http://127.0.0.1:8090"
echo ""
echo "Write-stress (distribute across all nodes):"
echo "  ./bin/trafficgen -write-stress -urls 'http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083,http://127.0.0.1:8084,http://127.0.0.1:8085,http://127.0.0.1:8086,http://127.0.0.1:8087,http://127.0.0.1:8088,http://127.0.0.1:8089,http://127.0.0.1:8090'"
echo ""
echo "Press Ctrl+C to stop all nodes"

trap "echo 'Stopping cluster...'; kill $PIDS 2>/dev/null; wait 2>/dev/null; echo 'Done.'" EXIT

wait
