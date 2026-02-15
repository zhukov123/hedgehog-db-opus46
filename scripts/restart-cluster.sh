#!/bin/bash
set -e

cd "$(dirname "$0")/.."

echo "Stopping all HedgehogDB instances..."
pkill -f "hedgehogdb" 2>/dev/null || true
sleep 2
# Ensure nothing is still running on our ports (requires lsof on macOS/Linux)
for port in 8081 8082 8083 8080; do
  pid=$(lsof -ti :$port 2>/dev/null) || true
  if [ -n "$pid" ]; then
    echo "Killing process on port $port (PID $pid)"
    kill $pid 2>/dev/null || true
    sleep 1
  fi
done
sleep 1

echo "Starting cluster..."
exec bash scripts/start-cluster.sh
