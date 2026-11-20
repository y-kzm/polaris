#!/bin/bash
# Build and run the RA controller backend alone (API only, no modules).
# To start the whole controller use ../server.sh or `docker compose up`.
# The backend is configured by environment variables; see cmd/server/main.go.
set -e
cd "$(dirname "$0")"
go build -o ra-controller ./cmd/server/
exec ./ra-controller
