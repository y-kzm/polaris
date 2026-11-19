#!/bin/sh
# The lab "Internet": one web server reachable at two service addresses.
#   2001:db8:f1::1  service S1 (inside the policy prefix 2001:db8:f1::/48)
#   2001:db8:f2::1  service S2 (reached through the default route)
set -eu

ip -6 addr add 2001:db8:f1::1/128 dev lo
ip -6 addr add 2001:db8:f2::1/128 dev lo

# Each ISP prefix is reachable only through the router of that ISP, so the
# reply to a connection returns through the router it came from.
ip -6 route replace 2001:db8:a::/64 via fd10::ff01
ip -6 route replace 2001:db8:b::/64 via fd10::ff02
ip -6 route replace 2001:db8:c::/64 via fd10::ff03

exec nginx -g 'daemon off;'
