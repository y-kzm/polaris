#!/bin/bash
# One-time setup script for the go-ra-server agent.
# Configures IPv6 kernel parameters, NPTv6, NDP proxy (ndppd),
# builds the gorad binary, and installs it as a systemd service.

set -euo pipefail

# --- Configuration ---
# Override with environment variables before running, or edit these defaults.
INSTALL_DIR="${INSTALL_DIR:-/opt/go-ra-server}"
GRPC_ADDR="${GRPC_ADDR:-localhost:50051}"
SERVICE_NAME="go-ra-server"
SERVICE_USER="gorad"

# NPTv6 parameters — set to match your network topology.
WAN_PREFIX="${WAN_PREFIX:-2001:2f8:1c0:7040::/64}"
LAN_PREFIX="${LAN_PREFIX:-2001:cafe:dead:beef::/64}"
WAN_IF="${WAN_IF:-enp2s0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=================================="
echo "   go-ra-server Setup Script      "
echo "=================================="
echo "Install directory : $INSTALL_DIR"
echo "gRPC address      : $GRPC_ADDR"
echo "WAN prefix        : $WAN_PREFIX"
echo "LAN prefix        : $LAN_PREFIX"
echo "WAN interface     : $WAN_IF"
echo

if [[ $EUID -ne 0 ]]; then
  echo "Error: must be run as root." >&2
  exit 1
fi

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

(cd "$GORA_SRC" && go build -o gorad ./cmd/gorad)
echo "Built: $GORA_SRC/gorad"
echo

# --- Deploy ---
echo "=== Deploy ==="
mkdir -p "$INSTALL_DIR"

install -m 755 "$GORA_SRC/gorad" "$INSTALL_DIR/gorad"
echo "Installed binary: $INSTALL_DIR/gorad"

if [[ ! -f "$INSTALL_DIR/config.yaml" ]]; then
  install -m 644 "$GORA_SRC/config.yaml" "$INSTALL_DIR/config.yaml"
  echo "Installed default config: $INSTALL_DIR/config.yaml"
  echo "  -> Edit this file to match your network before starting the service."
else
  echo "config.yaml already exists — skipping to preserve your settings."
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

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
echo "Service '$SERVICE_NAME' installed and started."
echo

echo "=================================="
echo "  Setup complete!"
echo ""
echo "  Check service status:"
echo "    systemctl status $SERVICE_NAME"
echo "    journalctl -u $SERVICE_NAME -f"
echo ""
echo "  Check RA daemon status:"
echo "    gora status"
echo "=================================="
