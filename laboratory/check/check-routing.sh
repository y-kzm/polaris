#!/bin/sh
# check-routing.sh — Verify RIO-based route for the server prefix on each host.
#
# After apply-policy.sh, each host in a policy group should have a specific
# route to fd10::/64 (the server network) via the assigned router's link-local.
# Hosts without a policy have no specific route (traffic falls back to default).
#
# Usage (from repo root):
#   sh laboratory/check/check-routing.sh

PROJECT="polaris-lab"
PREFIX="fd10::/64"

printf "Specific route to %s\n\n" "$PREFIX"
printf "%-10s  %s\n" "HOST" "NEXT HOP (RIO route)"
printf "%-10s  %s\n" "----------" "---------------------------------------------"

for i in $(seq 1 10); do
    host=$(printf "host%02d" "$i")

    gw=$(docker exec "${PROJECT}-${host}-1" \
         ip -6 route show "$PREFIX" 2>/dev/null \
         | awk '/via/{print $3; exit}')
    gw="${gw%%%*}"

    printf "%-10s  %s\n" "$host" "${gw:-(no RIO route — not in any policy group)}"
done
