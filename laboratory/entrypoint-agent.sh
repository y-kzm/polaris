#!/bin/sh
# Site-exit router of the lab: NPTv6, the go-ra agent, and self-registration
# with the RA controller.
#
# Required environment:
#   ROUTER_LABEL   name shown in the UI, e.g. "Router(a)"
#   ISP_PREFIX     the /64 this router's ISP delegated, e.g. 2001:db8:a::/64
#   POLICY_LL      link-local source address of the policy RAs, e.g. fe80::face:1
set -eu

LAN_PREFIX="fc00:cafe:dead:beef::/64"
INTERNET_PREFIX="2001:db8:f0::/44"   # the lab "Internet", behind the web server
WEB_SERVER="fd10::64"
RA_INTERVAL_MS="${RA_INTERVAL_MS:-3000}"
HOSTNAME=$(hostname)

sysctl -qw net.ipv6.conf.all.forwarding=1 2>/dev/null || true

# Docker does not guarantee the eth0/eth1/eth2 order, so find each interface
# by the prefix of its network.
iface_of() {
    ip -6 -br addr | awk -v p="$1" 'index($0, p) {split($1, a, "@"); print a[1]; exit}'
}
LAN_IF=$(iface_of "fc00:cafe:dead:beef:")
WAN_IF=$(iface_of "fd10::")
if [ -z "$LAN_IF" ] || [ -z "$WAN_IF" ]; then
    echo "[$HOSTNAME] cannot find the internal or upstream interface" >&2
    exit 1
fi

# ── Upstream: route to the Internet and NPTv6 (RFC 6296) ─────────────────────
# All hosts share LAN_PREFIX; each router rewrites it to its own ISP prefix,
# so the web server can tell which ISP a connection came through.
ip -6 route replace "$INTERNET_PREFIX" via "$WEB_SERVER" dev "$WAN_IF"
ip6tables -t mangle -A POSTROUTING -o "$WAN_IF" -s "$LAN_PREFIX" -j SNPT \
    --src-pfx "$LAN_PREFIX" --dst-pfx "$ISP_PREFIX"
ip6tables -t mangle -A PREROUTING -i "$WAN_IF" -d "$ISP_PREFIX" -j DNPT \
    --src-pfx "$ISP_PREFIX" --dst-pfx "$LAN_PREFIX"

# ── Internal: two link-local addresses ───────────────────────────────────────
# BASE_LL sends the baseline RA (all hosts, preference medium). POLICY_LL
# sends only the policy RAs. A host keeps one default-router entry per source
# address, so the two never overwrite each other.
BASE_LL=$(ip -6 addr show dev "$LAN_IF" scope link | awk '/inet6/ {sub("/.*", "", $2); print $2; exit}')
if [ -z "$BASE_LL" ]; then
    echo "[$HOSTNAME] $LAN_IF has no link-local address" >&2
    exit 1
fi
ip -6 addr replace "${POLICY_LL}/64" dev "$LAN_IF" nodad
LAN_ADDR=$(ip -6 addr show dev "$LAN_IF" scope global | awk '/inet6/ {sub("/.*", "", $2); print $2; exit}')

# Baseline RA (config #0). sourceAddress is set explicitly because the
# interface now has two link-local addresses. The controller adds the policy
# configurations (ID >= 1) over gRPC.
cat > /config.yaml <<YAML
interfaces:
- id: 0
  description: baseline
  name: ${LAN_IF}
  sourceAddress: ${BASE_LL}
  raIntervalMilliseconds: ${RA_INTERVAL_MS}
  currentHopLimit: 64
  preference: medium
  routerLifetimeSeconds: 1800
  reachableTimeMilliseconds: 30000
  retransmitTimeMilliseconds: 1000
  prefixes:
  - prefix: "${LAN_PREFIX}"
    onLink: true
    autonomous: true
    validLifetimeSeconds: 2592000
    preferredLifetimeSeconds: 604800
YAML

# ── Neighbor discovery for the Clients tab ───────────────────────────────────
# snmpd exposes the NDP cache (RFC 4293 ipNetToPhysicalTable); pinging the
# all-nodes group keeps the cache populated.
mkdir -p /etc/snmp
printf 'rocommunity  public default\nrocommunity6 public default\n' > /etc/snmp/snmpd.conf
snmpd -C -c /etc/snmp/snmpd.conf -Lf /dev/null &
(while true; do
    ping6 -c 2 -I "$BASE_LL" "ff02::1%${LAN_IF}" >/dev/null 2>&1 || true
    sleep 20
done) &

# ── Self-registration ────────────────────────────────────────────────────────
# Repeated, so that the router re-registers after the controller (whose
# database is in memory) is restarted.
(
    api="http://controller-backend:8080/api"
    nc="http://neighbor-collector:8083/api"
    while true; do
        if routers=$(curl -sf "$api/routers" 2>/dev/null) \
           && ! echo "$routers" | grep -q "\"address\":\"${LAN_ADDR}\""; then
            curl -sf -X POST "$api/routers" -H "Content-Type: application/json" -d "{
                \"name\": \"${ROUTER_LABEL}\", \"address\": \"${LAN_ADDR}\",
                \"interface\": \"${LAN_IF}\", \"policy_source_address\": \"${POLICY_LL}\",
                \"ra_interval_milliseconds\": ${RA_INTERVAL_MS}, \"current_hop_limit\": 64,
                \"router_lifetime_seconds\": 1800,
                \"reachable_time_milliseconds\": 30000, \"retransmit_time_milliseconds\": 1000
            }" >/dev/null \
                && echo "[$HOSTNAME] registered ${ROUTER_LABEL} address=${LAN_ADDR} if=${LAN_IF} policy-ll=${POLICY_LL}" \
                || echo "[$HOSTNAME] registration failed" >&2
        fi
        # Tell the neighbor collector which interface faces the hosts and
        # which address is the router itself.
        curl -sf -X PUT "$nc/targets/${HOSTNAME}" -H "Content-Type: application/json" \
            -d "{\"ifname\":\"${LAN_IF}\",\"self_ll\":\"${BASE_LL}\",\"label\":\"${ROUTER_LABEL}\"}" \
            >/dev/null 2>&1 || true
        sleep 5
    done
) &

echo "[$HOSTNAME] ${ROUTER_LABEL}: lan=${LAN_IF} wan=${WAN_IF} isp=${ISP_PREFIX} base-ll=${BASE_LL} policy-ll=${POLICY_LL}"
# 0.0.0.0 makes the gRPC server reachable from the controller.
exec /gorad -f /config.yaml -a 0.0.0.0:50051
