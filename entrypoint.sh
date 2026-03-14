#!/bin/sh
set -e

# POD_NAME and NAMESPACE are set by Kubernetes Downward API (e.g. hedgehogdb-0, hedgehogdb-1)
ORDINAL="${POD_NAME##*-}"
BASE="${POD_NAME%-*}"

SEED_NODES=""
if [ -n "$ORDINAL" ] && [ "$ORDINAL" -gt 0 ] 2>/dev/null; then
  i=0
  while [ "$i" -lt "$ORDINAL" ]; do
    if [ -n "$SEED_NODES" ]; then
      SEED_NODES="${SEED_NODES},${BASE}-${i}.${BASE}:8080"
    else
      SEED_NODES="${BASE}-${i}.${BASE}:8080"
    fi
    i=$(( i + 1 ))
  done
fi

CONFIG_ARG=""
if [ -f /etc/hedgehogdb/config.json ]; then
  CONFIG_ARG="-config /etc/hedgehogdb/config.json"
fi

exec /hedgehogdb \
  $CONFIG_ARG \
  -node-id "${POD_NAME:-node-1}" \
  -bind "0.0.0.0:8080" \
  -data-dir "/data" \
  ${SEED_NODES:+-seed-nodes "$SEED_NODES"}
