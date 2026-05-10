#!/bin/sh
# Add return routes to the internal segment via all three routers (ECMP).
# ::ff01/02/03 are well above Docker's auto-allocation range (::2..::f).
for router in fd10::ff01 fd10::ff02 fd10::ff03; do
    ip -6 route add fc00:cafe:dead:beef::/64 via "$router" 2>/dev/null || true
done
exec nginx -g 'daemon off;'
