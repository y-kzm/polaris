#!/bin/bash
set -e
cd "$(dirname "$0")"

export ADDR="${ADDR:-:8082}"

GOTOOLCHAIN=local go build -o endpoint-collector-static .
exec ./endpoint-collector-static
