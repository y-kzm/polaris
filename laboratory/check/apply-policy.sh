#!/bin/sh
# apply-policy.sh — Assign a host group to a site-exit router via RIO.
#
# Sends a unicast RA containing a Route Information Option (RIO) that tells
# each host to use the specified router for traffic to the server prefix.
# RIO-based control works reliably across OS implementations (RFC 4191).
#
# Index N maps to the polaris-lab-hostNN-1 container — i.e. "1-5" means
# host01..host05 (the link-local of each container is resolved at runtime).
#
# Example two-group scenario:
#   Group 1 (host 01-05) -> Router(a) : fd10::/64   (server network)
#   Group 2 (host 06-10) -> Router(b) : fd10::/64
#
# Usage (from repo root):
#   sh laboratory/check/apply-policy.sh <router> <hosts>
#
# Examples:
#   sh laboratory/check/apply-policy.sh "Router(a)" 1-5
#   sh laboratory/check/apply-policy.sh "Router(b)" 6-10
#   sh laboratory/check/apply-policy.sh "Router(c)" all

PROJECT="polaris-lab"
SERVER_PREFIX="fd10::/64"   # RIO destination — the server network
HOST_COUNT=10

ROUTER="${1:?Usage: $0 <router-name> <hosts>}"
HOSTS="${2:?Usage: $0 <router-name> <hosts>}"

BACKEND_IP=$(docker inspect "${PROJECT}-controller-backend-1" \
    --format '{{(index .NetworkSettings.Networks "polaris-lab_mgmt").IPAddress}}' 2>/dev/null)
[ -z "$BACKEND_IP" ] && { echo "ERROR: controller-backend is not running" >&2; exit 1; }

# Parse index expression ("1-5" / "1,3,5" / "all") into a sorted list of
# integers, then resolve each N to polaris-lab-hostNN-1's link-local address
# by querying the container directly — guarantees that "1-5" means host01..05.
INDICES=$(python3 -c "
import sys
sel = '$HOSTS'
n_max = $HOST_COUNT
if sel == 'all':
    print(' '.join(str(i) for i in range(1, n_max + 1)))
else:
    out = []
    for part in sel.split(','):
        if '-' in part:
            a, b = part.split('-', 1)
            out += range(int(a), int(b) + 1)
        elif part:
            out.append(int(part))
    print(' '.join(str(i) for i in sorted(set(out)) if 1 <= i <= n_max))
")

MEMBERS=""
for n in $INDICES; do
    cname=$(printf "${PROJECT}-host%02d-1" "$n")
    ll=$(docker exec "$cname" ip -6 addr show eth0 2>/dev/null \
         | awk '/inet6 fe80/ {sub("/.*","", $2); print $2; exit}')
    if [ -z "$ll" ]; then
        echo "WARNING: could not resolve link-local for $cname (skipped)" >&2
        continue
    fi
    MEMBERS="${MEMBERS}${MEMBERS:+ }${ll}"
done

[ -z "$MEMBERS" ] && { echo "ERROR: no hosts resolved" >&2; exit 1; }

python3 - "$BACKEND_IP" "$ROUTER" "$SERVER_PREFIX" "$INDICES" "$MEMBERS" <<'PYEOF'
import sys, json, urllib.request

api      = f"http://{sys.argv[1]}:8080"
router_sel, prefix = sys.argv[2], sys.argv[3]
indices  = sys.argv[4].split()
members  = sys.argv[5].split()

def call(method, path, body=None):
    data = json.dumps(body).encode() if body else b""
    req  = urllib.request.Request(f"{api}{path}", data=data,
           headers={"Content-Type": "application/json"} if data else {}, method=method)
    with urllib.request.urlopen(req) as r:
        raw = r.read()
        return json.loads(raw) if raw else {}

routers = call("GET", "/api/routers")
router  = next((r for r in routers if router_sel in (r["name"], r["address"])), None)
if not router:
    print(f"ERROR: '{router_sel}' not found. Available: {[r['name'] for r in routers]}")
    sys.exit(1)

print(f"Router : {router['name']}  ({router['address']})")
print(f"RIO    : {prefix}")
print(f"Members: {len(members)} host(s)")
for n, m in zip(indices, members):
    print(f"  host{int(n):02d}  {m}")
print()

grp  = call("POST", "/api/groups", {"name": f"{router['name']}-group", "members": members})
rule = call("POST", "/api/rules",  {"nexthop": router["address"], "entries": [{"value": prefix}]})
call("PUT", f"/api/groups/{grp['id']}/rules", {"rules": [rule["id"]]})
res  = call("POST", "/api/policy/apply")
print("Apply:", "OK" if res.get("success") else "PARTIAL")
PYEOF

echo ""
echo "Waiting for RA propagation (5 s)..."
sleep 5
echo ""
sh "$(dirname "$0")/check-routing.sh"
