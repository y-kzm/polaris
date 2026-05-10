#!/bin/sh
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true

# Detect the internal-segment interface by its known prefix
# (fc00:cafe:dead:beef::/64), regardless of Docker's eth assignment order.
RA_IFACE=$(ip -6 -br addr 2>/dev/null | awk '/fc00:cafe:dead:beef/ {split($1,a,"@"); print a[1]}' | head -1)
if [ -z "$RA_IFACE" ]; then
    echo "WARNING: could not detect internal interface; defaulting to eth0" >&2
    RA_IFACE=eth0
fi
IFACE="$RA_IFACE"

# SNMP daemon — exposes RFC 4293 ipNetToPhysicalTable so the neighbor-collector
# can read this router's NDP cache via SNMPv2c community "public".
mkdir -p /etc/snmp
cat > /etc/snmp/snmpd.conf <<EOF
rocommunity  public  default
rocommunity6 public  default
EOF
snmpd -C -c /etc/snmp/snmpd.conf -Lf /dev/null &

# Baseline RA config (config #0): multicast to all hosts on the internal
# segment, DRP=medium.  The RA controller pushes per-host unicast configs
# (ID ≥ 1) via gRPC when a policy is applied.
cat > /config.yaml <<EOF
interfaces:
- id: 0
  name: ${IFACE}
  raIntervalMilliseconds: ${RA_INTERVAL_MS:-3000}
  currentHopLimit: 64
  preference: medium
  routerLifetimeSeconds: 1800
  reachableTimeMilliseconds: 30000
  retransmitTimeMilliseconds: 1000
  disableRSReply: false
  prefixes:
  - prefix: "fc00:cafe:dead:beef::/64"
    onLink: true
    autonomous: true
    validLifetimeSeconds: 2592000
    preferredLifetimeSeconds: 604800
EOF

# Self-register with the RA controller once it becomes reachable.
# The hostname (router1/2/3) is the address the controller uses to connect via
# gRPC, and IFACE is the internal interface detected above.
HOSTNAME=$(hostname)
case "$HOSTNAME" in
    router1) ROUTER_LABEL="Router(a)" ;;
    router2) ROUTER_LABEL="Router(b)" ;;
    router3) ROUTER_LABEL="Router(c)" ;;
    *)       ROUTER_LABEL="$HOSTNAME" ;;
esac

# Use the static GUA on the internal interface as the router address so it
# appears as a real IPv6 address in the UI (not a Docker hostname).
INTERNAL_ADDR=$(ip -6 addr show dev "${IFACE}" 2>/dev/null \
    | awk '/fc00:cafe:dead:beef/ {sub("/.*","", $2); print $2; exit}')
ROUTER_ADDR="${INTERNAL_ADDR:-${HOSTNAME}}"

(
    until curl -sf "http://controller-backend:8080/api/routers" >/dev/null 2>&1; do
        sleep 2
    done
    if curl -sf "http://controller-backend:8080/api/routers" 2>/dev/null \
       | grep -q "\"address\":\"${ROUTER_ADDR}\""; then
        echo "[${HOSTNAME}] already registered (${ROUTER_ADDR})"
    else
        curl -sf -X POST "http://controller-backend:8080/api/routers" \
            -H "Content-Type: application/json" \
            -d "{\"name\":\"${ROUTER_LABEL}\",\"address\":\"${ROUTER_ADDR}\",\"interface\":\"${IFACE}\",\"ra_interval_milliseconds\":3000,\"current_hop_limit\":64,\"router_lifetime_seconds\":1800,\"reachable_time_milliseconds\":30000,\"retransmit_time_milliseconds\":1000}" \
            >/dev/null 2>&1 \
        && echo "[${HOSTNAME}] registered address=${ROUTER_ADDR} if=${IFACE}" \
        || echo "[${HOSTNAME}] registration failed" >&2
    fi
    # Tell the neighbor-collector which interface is the internal segment and
    # this router's own link-local, so it can correctly classify router entries.
    SELF_LL=$(ip -6 addr show dev "${IFACE}" 2>/dev/null \
              | awk '/inet6 fe80/ {sub("/.*","", $2); print $2; exit}')
    until curl -sf "http://neighbor-collector:8083/api/neighbors" >/dev/null 2>&1; do
        sleep 2
    done
    curl -sf -X PUT "http://neighbor-collector:8083/api/targets/${HOSTNAME}" \
        -H "Content-Type: application/json" \
        -d "{\"ifname\":\"${IFACE}\",\"self_ll\":\"${SELF_LL}\",\"label\":\"${ROUTER_LABEL}\"}" >/dev/null 2>&1 \
    && echo "[${HOSTNAME}] neighbor-collector ifname=${IFACE} label=${ROUTER_LABEL}" \
    || echo "[${HOSTNAME}] neighbor-collector update failed" >&2
) &

# Ping the all-nodes multicast group to warm up the NDP table.
sleep 1
ping6 -c 5 "ff02::1%${IFACE}" >/dev/null 2>&1 || true

# Periodically re-ping to keep NDP entries alive for SNMP collection.
(while true; do
    sleep 25
    ping6 -c 3 "ff02::1%${IFACE}" >/dev/null 2>&1 || true
done) &

# -a 0.0.0.0:50051 makes the gRPC server reachable from the controller
exec /gorad -f /config.yaml -a 0.0.0.0:50051
