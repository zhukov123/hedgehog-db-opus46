#!/bin/bash
set -e

BINARY="./bin/hedgehogdb"

if [ ! -f "$BINARY" ]; then
    echo "Building..."
    make build
fi

echo "Starting 3-node HedgehogDB cluster..."

# Create data directories
mkdir -p data/node1 data/node2 data/node3

# Start node 1
$BINARY \
    -node-id node-1 \
    -bind 127.0.0.1:8081 \
    -data-dir ./data/node1 &
PID1=$!
echo "Node 1 started (PID $PID1) on :8081"

sleep 1

# Start node 2
$BINARY \
    -node-id node-2 \
    -bind 127.0.0.1:8082 \
    -data-dir ./data/node2 \
    -seed-nodes 127.0.0.1:8081 &
PID2=$!
echo "Node 2 started (PID $PID2) on :8082"

sleep 1

# Start node 3
$BINARY \
    -node-id node-3 \
    -bind 127.0.0.1:8083 \
    -data-dir ./data/node3 \
    -seed-nodes 127.0.0.1:8081,127.0.0.1:8082 &
PID3=$!
echo "Node 3 started (PID $PID3) on :8083"

echo ""
echo "Cluster is running!"
echo "  Node 1: http://127.0.0.1:8081"
echo "  Node 2: http://127.0.0.1:8082"
echo "  Node 3: http://127.0.0.1:8083"
echo ""
echo "Press Ctrl+C to stop all nodes"

trap "echo 'Stopping cluster...'; kill $PID1 $PID2 $PID3 2>/dev/null; wait; echo 'Done.'" EXIT

wait
