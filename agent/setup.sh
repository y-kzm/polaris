#!/bin/bash
# One-time setup script for the go-ra-server agent.
# Configures IPv6 kernel parameters, NPTv6, NDP proxy (ndppd),
# builds the gorad binary, and installs it as a systemd service.
# With SNMP_COMMUNITY it also sets up snmpd for the RA controller.

set -euo pipefail

# --- Configuration ---
# Settings come from, in this order of precedence:
#   1. environment variables,
#   2. a settings file (KEY=VALUE lines): -c FILE, or ./setup.conf if it exists,
#   3. the defaults below.
# See setup.conf.example for every setting.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SETTINGS="INSTALL_DIR GRPC_ADDR WAN_PREFIX LAN_PREFIX WAN_IF LAN_IF POLICY_LL
RA_INTERVAL_MS ROUTER_LIFETIME_S CUR_HOP_LIMIT REACHABLE_TIME_MS RETRANS_TIME_MS RA_MTU
PREFIX_VALID_S PREFIX_PREFERRED_S RDNSS RDNSS_LIFETIME_S OVERWRITE_CONFIG GO
SNMP_COMMUNITY SNMP_ALLOW_FROM SNMP_LISTEN"

usage() {
  cat << USAGE
Usage: sudo ./setup.sh [-c FILE] [--print-config]

  -c FILE          read the settings from FILE (default: ./setup.conf, if present)
  --print-config   show the resolved settings and the files that would be
                   written (config.yaml, snmpd.conf), then exit without
                   changing anything
USAGE
}

CONF_FILE=""
PRINT_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -c) [[ $# -ge 2 ]] || { echo "Error: -c needs a file name." >&2; exit 1; }
        CONF_FILE="$2"; shift 2 ;;
    --print-config) PRINT_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Error: unknown argument '$1'." >&2; usage >&2; exit 1 ;;
  esac
done
if [[ -z "$CONF_FILE" && -f "$SCRIPT_DIR/setup.conf" ]]; then
  CONF_FILE="$SCRIPT_DIR/setup.conf"
fi

# The settings decide what root does (among others, which Go binary it runs),
# so a file that someone else could have written is refused: it must belong
# to root or to the user who called sudo, and must not be writable by group
# or others.
trust_problem() {
  local path="$1" owner mode
  if owner=$(stat -c '%u' "$path" 2>/dev/null) && mode=$(stat -c '%a' "$path" 2>/dev/null); then
    :                                             # GNU stat
  else
    owner=$(stat -f '%u' "$path"); mode=$(stat -f '%Lp' "$path")   # BSD stat
  fi
  if [[ "$owner" != 0 && "$owner" != "${SUDO_UID:-$(id -u)}" ]]; then
    echo "belongs to uid $owner, not to root or to you"
  elif (( (8#$mode & 8#022) != 0 )); then
    echo "is writable by group or others (mode $mode); run: chmod go-w $path"
  fi
}
check_trusted() {
  local problem
  problem=$(trust_problem "$1")
  if [[ -n "$problem" ]]; then
    echo "Error: the $2 $1 $problem." >&2; exit 1
  fi
}

# Read KEY=VALUE lines. The file is parsed, not executed: it is read as root.
# A value may be quoted; text after " #" is a comment. A variable that is
# set in the environment keeps its value.
load_conf() {
  local file="$1" line key value n=0 seen=""
  local re_dq='^"([^"]*)"[[:space:]]*(#.*)?$'
  local re_sq="^'([^']*)'[[:space:]]*(#.*)?\$"
  [[ -f "$file" ]] || { echo "Error: settings file not found: $file" >&2; exit 1; }
  check_trusted "$file" "settings file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    n=$((n + 1))
    line="${line%$'\r'}"
    [[ "$line" =~ ^[[:space:]]*(#.*)?$ ]] && continue
    if [[ ! "$line" =~ ^[[:space:]]*([A-Z_]+)[[:space:]]*=(.*)$ ]]; then
      echo "Error: $file:$n: expected KEY=VALUE, got: $line" >&2; exit 1
    fi
    key="${BASH_REMATCH[1]}"; value="${BASH_REMATCH[2]}"
    if [[ " $(echo $SETTINGS) " != *" $key "* ]]; then
      echo "Error: $file:$n: unknown setting '$key'." >&2; exit 1
    fi
    value="${value#"${value%%[![:space:]]*}"}"                 # leading blanks
    [[ "$value" == \#* ]] && value=""                           # nothing but a comment
    if [[ "$value" =~ $re_dq || "$value" =~ $re_sq ]]; then
      value="${BASH_REMATCH[1]}"
    else
      value="${value%%[[:space:]]#*}"                          # trailing comment
      value="${value%"${value##*[![:space:]]}"}"               # trailing blanks
    fi
    if [[ " $seen " == *" $key "* ]]; then
      echo "Error: $file:$n: '$key' is set twice." >&2; exit 1
    fi
    seen="$seen $key"
    if [[ " $FROM_ENV " != *" $key "* ]]; then
      printf -v "$key" '%s' "$value"
    fi
  done < "$file"
}
FROM_ENV=""
for name in $SETTINGS; do
  if [[ -n "${!name+x}" ]]; then FROM_ENV="$FROM_ENV $name"; fi
done
if [[ -n "$CONF_FILE" ]]; then
  load_conf "$CONF_FILE"
fi

INSTALL_DIR="${INSTALL_DIR:-/opt/go-ra-server}"
GRPC_ADDR="${GRPC_ADDR:-localhost:50051}"
SERVICE_NAME="go-ra-server"
SERVICE_USER="gorad"

# NPTv6 parameters. They describe this router's network and have no default:
# a wrong prefix or interface would install translation rules for someone
# else's addresses.
WAN_PREFIX="${WAN_PREFIX:-}"
LAN_PREFIX="${LAN_PREFIX:-}"
WAN_IF="${WAN_IF:-}"
MISSING=""
for name in WAN_IF WAN_PREFIX LAN_PREFIX; do
  if [[ -z "${!name}" ]]; then MISSING="$MISSING $name"; fi
done
if [[ -n "$MISSING" ]]; then
  echo "Error: required setting(s) not set:$MISSING" >&2
  echo "  Write them to a settings file:  cp setup.conf.example setup.conf  (then edit it)," >&2
  echo "  or pass them as environment variables." >&2
  exit 1
fi

# LAN-facing interface. With it, setup writes the baseline RA configuration
# (config.yaml) from the settings below instead of installing go-ra's sample.
LAN_IF="${LAN_IF:-}"

# Policy RA source address — a second link-local address on the LAN-facing
# interface, used only for the policy RAs the controller requests. Hosts keep
# one default-router entry per RA source address, so sending policy RAs from
# the baseline address would make the two overwrite each other.
# Leave empty to skip (policy RAs then share the baseline address). Needs LAN_IF.
POLICY_LL="${POLICY_LL:-}"

# Baseline RA: sent to all hosts, preference medium, no routes. The defaults
# are those of RFC 4861 Section 6.2.1 (MaxRtrAdvInterval 600 s,
# AdvDefaultLifetime 3 * MaxRtrAdvInterval, AdvReachableTime and
# AdvRetransTimer 0, AdvLinkMTU 0 = no MTU option, prefix lifetimes 30 and
# 7 days) and RFC 8106 (RDNSS lifetime 3 * MaxRtrAdvInterval).
# gorad sends at a fixed interval; RA_INTERVAL_MS is that interval.
RA_INTERVAL_MS="${RA_INTERVAL_MS:-600000}"
ROUTER_LIFETIME_S="${ROUTER_LIFETIME_S:-1800}"
CUR_HOP_LIMIT="${CUR_HOP_LIMIT:-64}"
REACHABLE_TIME_MS="${REACHABLE_TIME_MS:-0}"
RETRANS_TIME_MS="${RETRANS_TIME_MS:-0}"
RA_MTU="${RA_MTU:-0}"                          # 0: no MTU option
PREFIX_VALID_S="${PREFIX_VALID_S:-2592000}"
PREFIX_PREFERRED_S="${PREFIX_PREFERRED_S:-604800}"
RDNSS="${RDNSS:-}"                             # comma-separated; empty: no RDNSS option
RDNSS_LIFETIME_S="${RDNSS_LIFETIME_S:-1800}"
OVERWRITE_CONFIG="${OVERWRITE_CONFIG:-no}"     # yes: replace an existing config.yaml (a backup is kept)

# The Go compiler. sudo replaces PATH (secure_path), so a Go installation in
# /usr/local/go or in the home directory of the invoking user is not found
# through PATH; the usual places are searched. Set GO to name it explicitly.
# The build itself runs as the user who called sudo, not as root (see below),
# and a compiler that someone else could have replaced is not used.
GO="${GO:-}"
find_go() {
  local candidate home=""
  if [[ -n "$GO" ]]; then
    [[ -f "$GO" && -x "$GO" ]] || { echo "Error: GO=$GO is not an executable file." >&2; exit 1; }
    check_trusted "$GO" "Go compiler"
    return
  fi
  if [[ -n "${SUDO_USER:-}" ]]; then
    home=$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6 || true)
  fi
  for candidate in "$(command -v go || true)" /usr/local/go/bin/go /usr/lib/go/bin/go /snap/bin/go \
                   ${home:+"$home/go/bin/go" "$home/.local/go/bin/go" "$home/sdk/go/bin/go"}; do
    if [[ -n "$candidate" && -f "$candidate" && -x "$candidate" && -z "$(trust_problem "$candidate")" ]]; then
      GO="$candidate"
      return
    fi
  done
  echo "Error: the Go compiler was not found (sudo does not keep your PATH)." >&2
  echo "  Set GO to its path, in setup.conf or as:  sudo GO=\$(command -v go) ./setup.sh" >&2
  exit 1
}

for name in RA_INTERVAL_MS ROUTER_LIFETIME_S CUR_HOP_LIMIT REACHABLE_TIME_MS RETRANS_TIME_MS RA_MTU \
            PREFIX_VALID_S PREFIX_PREFERRED_S RDNSS_LIFETIME_S; do
  if [[ ! "${!name}" =~ ^[0-9]+$ ]]; then
    echo "Error: $name must be a non-negative integer, got '${!name}'." >&2; exit 1
  fi
done
case "$OVERWRITE_CONFIG" in yes|no) ;; *) echo "Error: OVERWRITE_CONFIG must be yes or no." >&2; exit 1 ;; esac
if [[ "$OVERWRITE_CONFIG" == yes && -z "${LAN_IF:-}" ]]; then
  echo "Error: OVERWRITE_CONFIG=yes needs LAN_IF (the baseline configuration is written for it)." >&2; exit 1
fi
for name in WAN_PREFIX LAN_PREFIX; do
  if [[ ! "${!name}" =~ ^[0-9a-fA-F:]+/[0-9]+$ ]]; then
    echo "Error: $name must be an IPv6 prefix such as 2001:db8:1::/64, got '${!name}'." >&2; exit 1
  fi
done
# The directory is handed to the service user (chown -R), so it must be a
# directory of its own: at least two path segments, none of them "." or "..".
if [[ ! "$INSTALL_DIR" =~ ^(/[A-Za-z0-9_][A-Za-z0-9._-]*){2,}$ ]]; then
  echo "Error: INSTALL_DIR must be an absolute path with at least two segments" \
       "(such as /opt/go-ra-server), got '$INSTALL_DIR'." >&2; exit 1
fi
if [[ ! "$GRPC_ADDR" =~ ^[][A-Za-z0-9.:_-]+$ ]]; then
  echo "Error: GRPC_ADDR must be host:port, got '$GRPC_ADDR'." >&2; exit 1
fi
for name in WAN_IF LAN_IF; do
  if [[ -n "${!name}" && ! "${!name}" =~ ^[A-Za-z0-9._@-]+$ ]]; then
    echo "Error: $name is not an interface name: '${!name}'." >&2; exit 1
  fi
done
if [[ -n "$POLICY_LL" ]]; then
  if [[ -z "$LAN_IF" ]]; then
    echo "Error: POLICY_LL needs LAN_IF." >&2; exit 1
  fi
  if [[ ! "$POLICY_LL" =~ ^[fF][eE]80:[0-9a-fA-F:]*$ ]]; then
    echo "Error: POLICY_LL must be a link-local address (fe80::/10), got '$POLICY_LL'." >&2; exit 1
  fi
  # Canonical form (as "ip" prints it), so that it can be told from the
  # interface's own address below.
  if command -v python3 > /dev/null; then
    POLICY_LL=$(python3 -c 'import ipaddress, sys; print(ipaddress.IPv6Address(sys.argv[1]))' "$POLICY_LL" 2>/dev/null) || {
      echo "Error: POLICY_LL is not a valid IPv6 address." >&2; exit 1
    }
  fi
fi
RDNSS_LIST=()
if [[ -n "$RDNSS" ]]; then
  IFS=', ' read -r -a RDNSS_LIST <<< "$RDNSS"
  for addr in ${RDNSS_LIST[@]+"${RDNSS_LIST[@]}"}; do
    if [[ ! "$addr" =~ ^[0-9a-fA-F:]+$ ]]; then
      echo "Error: RDNSS: '$addr' is not an IPv6 address." >&2; exit 1
    fi
  done
fi

# SNMP agent (snmpd). The RA controller learns the hosts of the site link from
# this router's neighbor cache, which it reads over SNMP v2c. With
# SNMP_COMMUNITY, setup installs snmpd (if missing) and writes its
# configuration: read-only, only for the sources in SNMP_ALLOW_FROM, and only
# for the tables the controller reads. None of the three settings has a
# default: a well-known community, "any source" or "every address" (which
# includes the WAN side) would open the agent further than intended.
# Empty SNMP_COMMUNITY: snmpd is not touched.
SNMP_COMMUNITY="${SNMP_COMMUNITY:-}"
SNMP_ALLOW_FROM="${SNMP_ALLOW_FROM:-}"           # comma-separated addresses or prefixes of the controller
SNMP_LISTEN="${SNMP_LISTEN:-}"                   # agentAddress of snmpd, e.g. udp6:[2001:db8:ffff::a]:161
SNMP_CONF="/etc/snmp/snmpd.conf"
SNMP_MARK="# Written by polaris agent/setup.sh."
SNMP_SOURCES=()
if [[ -n "$SNMP_COMMUNITY" ]]; then
  # No leading "-" (snmpd would read it as an option) and no ":" or ","
  # (separators of the controller's SNMP_TARGETS list).
  if [[ ! "$SNMP_COMMUNITY" =~ ^[A-Za-z0-9][A-Za-z0-9._@%+=-]*$ ]]; then
    echo "Error: SNMP_COMMUNITY must start with a letter or digit and may contain" \
         "letters, digits and . _ @ % + = - only." >&2; exit 1
  fi
  # The community is the only credential of SNMP v2c.
  SNMP_LOWER=$(printf '%s' "$SNMP_COMMUNITY" | tr 'A-Z' 'a-z')
  case "$SNMP_LOWER" in
    public|private|community|snmp|snmpd|admin|default|password|secret|polaris|monitor|manager|cisco)
      echo "Error: SNMP_COMMUNITY is a well-known word; choose your own (for example: openssl rand -hex 12)." >&2; exit 1 ;;
  esac
  if [[ ${#SNMP_COMMUNITY} -lt 12 ]]; then
    echo "Error: SNMP_COMMUNITY must have at least 12 characters (for example: openssl rand -hex 12)." >&2; exit 1
  fi
  if ! command -v python3 > /dev/null; then
    echo "Error: python3 is needed to check SNMP_ALLOW_FROM." >&2; exit 1
  fi
  if [[ -z "$SNMP_ALLOW_FROM" ]]; then
    echo "Error: SNMP_COMMUNITY needs SNMP_ALLOW_FROM (the address of the RA controller)." >&2; exit 1
  fi
  if [[ "$SNMP_ALLOW_FROM" == *$'\n'* ]]; then
    echo "Error: SNMP_ALLOW_FROM must be a single line." >&2; exit 1
  fi
  IFS=', ' read -r -a SNMP_SOURCES <<< "$SNMP_ALLOW_FROM"
  if [[ ${#SNMP_SOURCES[@]} -eq 0 ]]; then
    echo "Error: SNMP_ALLOW_FROM names no address." >&2; exit 1
  fi
  for src in "${SNMP_SOURCES[@]}"; do
    if [[ ! "$src" =~ ^([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+|[0-9a-fA-F:]*:[0-9a-fA-F:]*)(/[0-9]+)?$ ]]; then
      echo "Error: SNMP_ALLOW_FROM: '$src' is not an IPv4 or IPv6 address or prefix." >&2; exit 1
    fi
    # snmpd skips a source it cannot parse and starts all the same, so check here.
    if ! python3 -c 'import ipaddress, sys; ipaddress.ip_network(sys.argv[1], strict=False)' "$src" 2>/dev/null; then
      echo "Error: SNMP_ALLOW_FROM: '$src' is not a valid address or prefix." >&2; exit 1
    fi
  done
  SNMP_ALLOW_FROM="${SNMP_SOURCES[*]}"
  if [[ -z "$SNMP_LISTEN" ]]; then
    echo "Error: SNMP_COMMUNITY needs SNMP_LISTEN: the address the controller polls, such as" \
         "udp6:[2001:db8:ffff::a]:161. (udp:161,udp6:161 listens on every address, the WAN side included.)" >&2; exit 1
  fi
  if [[ ! "$SNMP_LISTEN" =~ ^[][A-Za-z0-9.:,_%-]+$ ]]; then
    echo "Error: SNMP_LISTEN must be an snmpd agentAddress such as udp6:[2001:db8:ffff::a]:161, got '$SNMP_LISTEN'." >&2; exit 1
  fi
fi

# Write the snmpd configuration to stdout. $1: the community string.
# The view covers what controller/modules/neighbor-collector-snmp walks.
snmpd_config() {
  local community="$1" src
  cat << CONF
$SNMP_MARK Changes are lost when setup runs again.
# Read-only access for the RA controller, limited to the tables it reads:
# the neighbor cache (IP-MIB::ipNetToPhysicalTable), the interface names
# (IF-MIB::ifName, ifDescr) and the routes (IP-MIB::inetCidrRouteTable).
agentAddress $SNMP_LISTEN
view polaris included .1.3.6.1.2.1.4.35
view polaris included .1.3.6.1.2.1.31.1.1.1.1
view polaris included .1.3.6.1.2.1.2.2.1.2
view polaris included .1.3.6.1.2.1.4.24.7
CONF
  for src in "${SNMP_SOURCES[@]}"; do
    if [[ "$src" == *:* ]]; then
      echo "rocommunity6 $community $src -V polaris"
    else
      echo "rocommunity $community $src -V polaris"
    fi
  done
}

# The interface's own link-local address: the source of the baseline RA.
BASE_LL=""
if [[ -n "$LAN_IF" ]] && command -v ip > /dev/null; then
  BASE_LL=$(ip -6 -o addr show dev "$LAN_IF" scope link 2>/dev/null \
    | awk '{sub("/.*", "", $4); print $4}' | grep -vix "${POLICY_LL:-none}" | head -1 || true)
fi

# Write the baseline RA configuration to stdout.
baseline_config() {
  cat << YAML
# Baseline RA of this router, written by setup.sh. The RA controller adds the
# policy configurations (ID >= 1) over gRPC; they are not stored in this file.
interfaces:
- id: 0
  description: baseline
  name: "${LAN_IF}"
YAML
  if [[ -n "$POLICY_LL" ]]; then
    # Explicit, because the interface has two link-local addresses.
    echo "  sourceAddress: ${BASE_LL:-<link-local address of $LAN_IF>}"
  fi
  cat << YAML
  raIntervalMilliseconds: ${RA_INTERVAL_MS}
  currentHopLimit: ${CUR_HOP_LIMIT}
  managed: false
  other: false
  preference: medium
  routerLifetimeSeconds: ${ROUTER_LIFETIME_S}
  reachableTimeMilliseconds: ${REACHABLE_TIME_MS}
  retransmitTimeMilliseconds: ${RETRANS_TIME_MS}
YAML
  if [[ "$RA_MTU" != 0 ]]; then
    echo "  mtu: ${RA_MTU}"
  fi
  cat << YAML
  prefixes:
  - prefix: "${LAN_PREFIX}"
    onLink: true
    autonomous: true
    validLifetimeSeconds: ${PREFIX_VALID_S}
    preferredLifetimeSeconds: ${PREFIX_PREFERRED_S}
YAML
  if [[ ${#RDNSS_LIST[@]} -gt 0 ]]; then
    echo "  rdnsses:"
    echo "  - lifetimeSeconds: ${RDNSS_LIFETIME_S}"
    echo "    addresses:"
    for addr in "${RDNSS_LIST[@]}"; do
      echo "    - \"$addr\""
    done
  fi
  echo "  sendGoodbye: true"
}

echo "=================================="
echo "   go-ra-server Setup Script      "
echo "=================================="
echo "Settings file     : ${CONF_FILE:-(none)}"
echo "Install directory : $INSTALL_DIR"
echo "gRPC address      : $GRPC_ADDR"
echo "WAN prefix        : $WAN_PREFIX"
echo "LAN prefix        : $LAN_PREFIX"
echo "WAN interface     : $WAN_IF"
echo "LAN interface     : ${LAN_IF:-(not set — the sample config.yaml of go-ra is installed)}"
echo "Policy RA source  : ${POLICY_LL:-(not set — shares the baseline address)}"
if [[ -n "$LAN_IF" ]]; then
  echo "Baseline RA       : every ${RA_INTERVAL_MS} ms, Router Lifetime ${ROUTER_LIFETIME_S} s," \
       "MTU option $([[ "$RA_MTU" == 0 ]] && echo "(none)" || echo "$RA_MTU"), RDNSS ${RDNSS:-(none)}"
fi
if [[ -n "$SNMP_COMMUNITY" ]]; then
  echo "SNMP agent        : snmpd on $SNMP_LISTEN, read-only for $SNMP_ALLOW_FROM"
else
  echo "SNMP agent        : (not set up — SNMP_COMMUNITY is empty)"
fi
echo

if [[ $PRINT_ONLY -eq 1 ]]; then
  if [[ -n "$LAN_IF" ]]; then
    if [[ -n "$POLICY_LL" && -z "$BASE_LL" ]]; then
      echo "Note: $LAN_IF has no link-local address here, so sourceAddress is shown as a placeholder;"
      echo "      the real run stops with an error in this case."
    fi
    echo "--- $INSTALL_DIR/config.yaml ---"
    baseline_config
  else
    echo "LAN_IF is not set: no baseline configuration is generated."
  fi
  if [[ -n "$SNMP_COMMUNITY" ]]; then
    echo
    echo "--- $SNMP_CONF (the community string is not shown) ---"
    snmpd_config "<SNMP_COMMUNITY>"
  fi
  exit 0
fi

if [[ $EUID -ne 0 ]]; then
  echo "Error: must be run as root." >&2
  exit 1
fi
if [[ -n "$POLICY_LL" && -z "$BASE_LL" ]]; then
  echo "Error: $LAN_IF has no link-local address of its own; it is needed as the source of the baseline RA." >&2
  exit 1
fi
find_go
echo "Go compiler       : $GO"
echo

# --- Kernel parameters ---
echo "=== Kernel Parameters ==="
sysctl -w net.ipv6.conf.all.forwarding=1
sysctl -w net.ipv6.conf.all.accept_redirects=0
sysctl -w net.ipv6.conf.all.proxy_ndp=1

# Persist across reboots.
cat > /etc/sysctl.d/99-go-ra-server.conf << 'SYSCTL'
net.ipv6.conf.all.forwarding = 1
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.all.proxy_ndp = 1
SYSCTL
echo "Kernel parameters applied and persisted to /etc/sysctl.d/99-go-ra-server.conf."
echo

# --- NPTv6 ---
echo "=== NPTv6 (Network Prefix Translation for IPv6) ==="

# Remove existing rules to prevent duplicates.
ip6tables -t mangle -D POSTROUTING -o "$WAN_IF" -s "$LAN_PREFIX" -j SNPT \
  --src-pfx "$LAN_PREFIX" --dst-pfx "$WAN_PREFIX" 2>/dev/null || true
ip6tables -t mangle -D PREROUTING -i "$WAN_IF" -d "$WAN_PREFIX" -j DNPT \
  --src-pfx "$WAN_PREFIX" --dst-pfx "$LAN_PREFIX" 2>/dev/null || true

ip6tables -t mangle -A POSTROUTING -o "$WAN_IF" -s "$LAN_PREFIX" -j SNPT \
  --src-pfx "$LAN_PREFIX" --dst-pfx "$WAN_PREFIX"
ip6tables -t mangle -A PREROUTING -i "$WAN_IF" -d "$WAN_PREFIX" -j DNPT \
  --src-pfx "$WAN_PREFIX" --dst-pfx "$LAN_PREFIX"

echo "NPTv6 rules applied:"
ip6tables -t mangle -L -v -n
echo
echo "Note: persist these rules across reboots with netfilter-persistent or ip6tables-save."
echo

# --- NDP Proxy ---
echo "=== NDP Proxy (ndppd) ==="
if systemctl is-active --quiet ndppd.service; then
  echo "ndppd.service is already active."
else
  echo "ndppd.service is not running — starting and enabling..."
  systemctl start ndppd.service
  systemctl enable ndppd.service
  echo "ndppd.service started and enabled."
fi
echo

# --- Build gorad ---
echo "=== Build gorad ==="
GORA_SRC="$SCRIPT_DIR/go-ra"

if [[ ! -d "$GORA_SRC" ]]; then
  echo "Error: go-ra source directory not found at $GORA_SRC." >&2
  echo "  Initialise the submodule first:" >&2
  echo "    git submodule update --init" >&2
  exit 1
fi

# Build as the user who called sudo: the compiler, which may live in that
# user's home directory, then never runs as root, and the checkout and the Go
# caches keep their owner. Only a direct root login builds as root.
BUILD_USER="${SUDO_USER:-root}"
if [[ "$BUILD_USER" != root ]] && command -v runuser > /dev/null; then
  BUILD_HOME=$(getent passwd "$BUILD_USER" | cut -d: -f6)
  runuser -u "$BUILD_USER" -- env HOME="$BUILD_HOME" PATH="$(dirname "$GO"):/usr/local/bin:/usr/bin:/bin" \
    sh -c 'cd "$1" && "$2" build -o gorad ./cmd/gorad' sh "$GORA_SRC" "$GO"
  echo "Built as user $BUILD_USER."
else
  if [[ "$(stat -c '%u' "$GO")" != 0 ]]; then
    echo "Error: refusing to run $GO as root: it does not belong to root." >&2
    echo "  Run setup with sudo from your own account, so that the build runs as you." >&2
    exit 1
  fi
  (cd "$GORA_SRC" && "$GO" build -o gorad ./cmd/gorad)
fi
echo "Built: $GORA_SRC/gorad"
echo

# --- Deploy ---
echo "=== Deploy ==="
mkdir -p "$INSTALL_DIR"

install -m 755 "$GORA_SRC/gorad" "$INSTALL_DIR/gorad"
echo "Installed binary: $INSTALL_DIR/gorad"

CONFIG="$INSTALL_DIR/config.yaml"
# The directory belongs to the service user, so do not follow a link there.
if [[ -L "$CONFIG" ]]; then
  echo "Error: $CONFIG is a symbolic link; remove it or replace it by a file." >&2
  exit 1
fi
if [[ -f "$CONFIG" && "$OVERWRITE_CONFIG" != yes ]]; then
  echo "config.yaml already exists — skipping to preserve your settings."
  echo "  (set OVERWRITE_CONFIG=yes to write it again from the settings)"
elif [[ -n "$LAN_IF" ]]; then
  if [[ -f "$CONFIG" ]]; then
    BACKUP="$CONFIG.bak-$(date +%Y%m%d-%H%M%S)"
    cp -P -p "$CONFIG" "$BACKUP"
    echo "Kept the previous configuration as $BACKUP"
  fi
  TMP_CONFIG=$(mktemp "$CONFIG.XXXXXX")
  baseline_config > "$TMP_CONFIG"
  chmod 644 "$TMP_CONFIG"
  mv -f "$TMP_CONFIG" "$CONFIG"
  echo "Wrote the baseline RA configuration: $CONFIG"
elif [[ ! -f "$CONFIG" ]]; then
  install -m 644 "$GORA_SRC/config.yaml" "$CONFIG"
  echo "Installed go-ra's sample config: $CONFIG"
  echo "  -> It is an example (interface eth0, documentation prefixes, a sample route)."
  echo "     Edit it before use, or set LAN_IF and run setup again with OVERWRITE_CONFIG=yes."
fi
echo

# --- System user ---
echo "=== System User ==="
if ! id "$SERVICE_USER" &>/dev/null; then
  useradd -r -s /sbin/nologin -d "$INSTALL_DIR" "$SERVICE_USER"
  echo "Created system user '$SERVICE_USER'."
else
  echo "User '$SERVICE_USER' already exists."
fi
chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"
echo

# --- Systemd service ---
echo "=== Systemd Service ==="
SERVICE_FILE="$SCRIPT_DIR/systemd/$SERVICE_NAME.service"

if [[ ! -f "$SERVICE_FILE" ]]; then
  echo "Error: service file not found at $SERVICE_FILE." >&2
  exit 1
fi

install -m 644 "$SERVICE_FILE" "/etc/systemd/system/$SERVICE_NAME.service"

if [[ "$INSTALL_DIR" != "/opt/go-ra-server" ]]; then
  sed -i "s|/opt/go-ra-server|$INSTALL_DIR|g" "/etc/systemd/system/$SERVICE_NAME.service"
fi

if [[ "$GRPC_ADDR" != "localhost:50051" ]]; then
  sed -i "s|localhost:50051|$GRPC_ADDR|g" "/etc/systemd/system/$SERVICE_NAME.service"
fi

# --- Policy RA source address ---
# Assigned by a drop-in before every start of the service, so it survives
# reboots. "+" runs the command as root regardless of the service user.
DROPIN_DIR="/etc/systemd/system/$SERVICE_NAME.service.d"
if [[ -n "$POLICY_LL" ]]; then
  mkdir -p "$DROPIN_DIR"
  cat > "$DROPIN_DIR/policy-address.conf" << DROPIN
[Service]
ExecStartPre=+$(command -v ip) -6 addr replace $POLICY_LL/64 dev $LAN_IF nodad
DROPIN
  echo "Policy RA source address $POLICY_LL will be assigned to $LAN_IF on every start."
  echo "  In the RA controller, set this router's policy source address to $POLICY_LL."
  if ! grep -q "^  sourceAddress: " "$CONFIG"; then
    echo "  Warning: $CONFIG has no sourceAddress on the baseline instance."
    echo "           Add:  sourceAddress: ${BASE_LL:-<link-local address of $LAN_IF>}"
  fi
else
  rm -f "$DROPIN_DIR/policy-address.conf"
fi
echo

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
echo "Service '$SERVICE_NAME' installed and started."
echo

# --- SNMP agent ---
if [[ -n "$SNMP_COMMUNITY" ]]; then
  echo "=== SNMP agent (snmpd) ==="
  if ! command -v snmpd > /dev/null; then
    if command -v apt-get > /dev/null; then
      echo "snmpd is not installed — installing it with apt-get..."
      DEBIAN_FRONTEND=noninteractive apt-get install -y snmpd || {
        apt-get update        # stale or missing package lists
        DEBIAN_FRONTEND=noninteractive apt-get install -y snmpd
      }
    else
      echo "Error: snmpd is not installed and apt-get is not available; install net-snmp and run setup again." >&2
      exit 1
    fi
  fi
  mkdir -p "$(dirname "$SNMP_CONF")"
  if [[ -L "$SNMP_CONF" ]]; then
    echo "Error: $SNMP_CONF is a symbolic link; remove it or replace it by a file." >&2
    exit 1
  fi
  if [[ -e "$SNMP_CONF" && ! -f "$SNMP_CONF" ]]; then
    echo "Error: $SNMP_CONF is not a regular file." >&2
    exit 1
  fi
  # A configuration that setup did not write is kept as a backup.
  if [[ -f "$SNMP_CONF" ]] && ! head -1 "$SNMP_CONF" | grep -qF "$SNMP_MARK"; then
    SNMP_BACKUP="$SNMP_CONF.bak-$(date +%Y%m%d-%H%M%S)"
    cp -P -p "$SNMP_CONF" "$SNMP_BACKUP"
    echo "Kept the previous configuration as $SNMP_BACKUP"
  fi
  TMP_SNMP=$(mktemp "$SNMP_CONF.XXXXXX")      # mode 600: the file holds the community string
  snmpd_config "$SNMP_COMMUNITY" > "$TMP_SNMP"
  mv -f "$TMP_SNMP" "$SNMP_CONF"
  echo "Wrote $SNMP_CONF"
  systemctl enable snmpd
  systemctl restart snmpd
  sleep 1
  if ! systemctl is-active --quiet snmpd; then
    echo "Error: snmpd did not stay up; see: journalctl -u snmpd -n 30" >&2
    echo "  (a common cause is an address in SNMP_LISTEN that this router does not have)" >&2
    exit 1
  fi
  echo "snmpd enabled and restarted."
  echo "  In the RA controller (param.yaml, snmp_targets), add this router with the same"
  echo "  community (in quotes)${LAN_IF:+, ifname \"$LAN_IF\"}${BASE_LL:+ and self_ll \"$BASE_LL\"}."
  echo "  If a firewall is active, allow UDP port 161 from $SNMP_ALLOW_FROM."
  echo
fi

echo "=================================="
echo "  Setup complete!"
echo ""
echo "  Check service status:"
echo "    systemctl status $SERVICE_NAME"
echo "    journalctl -u $SERVICE_NAME -f"
echo ""
echo "  Check RA daemon status:"
echo "    gora status"
if [[ -n "$SNMP_COMMUNITY" ]]; then
  echo ""
  echo "  Check the SNMP agent (from the controller):"
  echo "    snmpwalk -v2c -c <community> <this router> 1.3.6.1.2.1.4.35"
fi
echo "=================================="
