#!/bin/bash
set -e
cd "$(dirname "$0")"

export ADDR="${ADDR:-:8084}"
export SNMP_TARGETS="${SNMP_TARGETS:-localhost:161:public}"
export POLL_INTERVAL="${POLL_INTERVAL:-30}"

GOTOOLCHAIN=local go build -o neighbor-collector-snmp .
exec ./neighbor-collector-snmp
