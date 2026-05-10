#!/bin/bash
set -e
cd "$(dirname "$0")"

export ADDR="${ADDR:-:8083}"

GOTOOLCHAIN=local go build -o neighbor-collector-static .
exec ./neighbor-collector-static
