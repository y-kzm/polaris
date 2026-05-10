#!/bin/bash
# Build and run the RA Controller backend server.
set -e
cd "$(dirname "$0")"

GOTOOLCHAIN=local go build -o ra-controller ./cmd/server/

export PORT="${PORT:-8080}"
export DB_PATH="${DB_PATH:-controller.db}"
export NEIGHBOR_API_URLS="${NEIGHBOR_API_URLS:-}"
export NEIGHBOR_SOURCE_NAMES="${NEIGHBOR_SOURCE_NAMES:-}"
export NEIGHBOR_IFNAME="${NEIGHBOR_IFNAME:-}"
export FETCH_INTERVAL="${FETCH_INTERVAL:-10}"
export FQDN_API_BASES="${FQDN_API_BASES:-}"

echo "Starting RA Controller backend on port $PORT"
./ra-controller
