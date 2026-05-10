#!/bin/bash
set -e
cd "$(dirname "$0")"
export ADDR="${ADDR:-:8000}"
go build -o endpoint-collector-ms365 .
exec ./endpoint-collector-ms365
