# go-ra-server Agent

An IPv6 Router Advertisement agent based on [go-ra](https://github.com/YutaroHayakawa/go-ra), deployed on each router node in the polaris system.

The agent runs `gorad` — the go-ra daemon — which sends IPv6 Router Advertisements (RA) to clients on the local network and exposes a gRPC API for runtime policy management. The [RA Controller](../controller) connects to this gRPC endpoint to push RA configuration dynamically. The agent also configures NPTv6 prefix translation and NDP proxy so that LAN-side prefixes are reachable from the WAN.

The `setup.sh` script handles the full one-time setup:
1. Applies IPv6 kernel parameters
2. Configures NPTv6 rules (LAN ↔ WAN prefix translation)
3. Starts NDP proxy (`ndppd`)
4. Builds and deploys the `gorad` binary
5. Installs and starts the systemd service

## Components

| Path | Role |
|------|------|
| `go-ra/` | go-ra source (git submodule — forked from YutaroHayakawa/go-ra) |
| `systemd/go-ra-server.service` | Systemd unit file |
| `setup.sh` | One-time setup script |

After `setup.sh` runs, `config.yaml` is deployed to `$INSTALL_DIR` (default `/opt/go-ra-server`).

## Prerequisites

- Linux with systemd
- Go (for building `gorad`)
- `ip6tables` with SNPT/DNPT target support (for NPTv6)
- `ndppd` (NDP proxy daemon)

## Quick start (manual / development)

```bash
# Initialise the submodule if you haven't already
git submodule update --init

# Build the daemon
cd go-ra
go build -o gorad ./cmd/gorad

# Edit the RA configuration
cp config.yaml my-config.yaml
# ... edit interfaces, prefixes, etc.

# Run as root (raw ICMPv6 socket required)
sudo ./gorad -f my-config.yaml

# Check status from another terminal
gora status
```

## Configuration

All RA settings are defined in `config.yaml` (deployed to `$INSTALL_DIR/config.yaml`).

```yaml
interfaces:
- id: 1
  name: eth0
  raIntervalMilliseconds: 600000   # 10 minutes (RFC 4861 max unsolicited interval)
  currentHopLimit: 64
  managed: false
  other: false
  routerLifetimeSeconds: 1800
  preference: medium
  reachableTimeMilliseconds: 30000
  retransmitTimeMilliseconds: 1000
  mtu: 1500

  prefixes:
  - prefix: "2001:db8::/64"
    onLink: true
    autonomous: true
    validLifetimeSeconds: 2592000    # 30 days
    preferredLifetimeSeconds: 604800 # 7 days

  routes:
  - prefix: "2001:db8:1::/48"
    lifetimeSeconds: 3600
    preference: high

  sendGoodbye: true
```

See [`go-ra/config.yaml`](go-ra/config.yaml) for a full reference with all options.

## Policy RA source address

The controller's policy RAs must not share a source address with the baseline
RA: a host keeps one default-router entry per source address, so the two
would overwrite each other's preference and lifetime. Give the LAN-facing
interface a second link-local address and use it only for policy RAs.

`setup.sh` does this when both variables are set:

```bash
sudo LAN_IF=eth1 POLICY_LL=fe80::face:1 \
     WAN_PREFIX=... LAN_PREFIX=... WAN_IF=eth0 ./setup.sh
```

It installs a systemd drop-in that assigns `POLICY_LL` to `LAN_IF` before
every start of the service. Two manual steps remain, and `setup.sh` prints
them:

1. In `config.yaml`, set `sourceAddress` on the baseline instance to the
   interface's original link-local address. With two link-local addresses,
   which one is used by default is not defined (it can be the one added
   later), so always name it explicitly.
2. In the RA controller, set the router's **Policy RA source address** to
   `POLICY_LL`.

`POLICY_LL` must be unique on the link. Any unused `fe80::/64` address works.
The controller refuses to apply a policy if the policy address is also the
source address of the baseline instance.

### Upgrading from a version without policy source addresses

Older controllers installed one configuration per rule, without the
`polaris-policy` description. The current controller does not touch
configurations it cannot identify as its own, so restart the agent
(`systemctl restart go-ra-server`) after upgrading: this drops the old
configurations, and the next **Apply** installs the new ones. The agent and
the controller must be upgraded together (`SendRA` is required).

## Systemd deployment

### Installation

**1. Initialise the submodule**

```bash
git submodule update --init
```

**2. Set your NPTv6 parameters and run setup**

```bash
sudo WAN_PREFIX="2001:db8:wan::/64" \
     LAN_PREFIX="fd00:cafe::/64"    \
     WAN_IF="eth0"                  \
     ./setup.sh
```

`setup.sh` accepts the following environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `INSTALL_DIR` | `/opt/go-ra-server` | Installation directory |
| `GRPC_ADDR` | `localhost:50051` | gRPC listen address for gorad |
| `WAN_PREFIX` | `2001:2f8:1c0:7040::/64` | WAN-side IPv6 prefix for NPTv6 |
| `LAN_PREFIX` | `2001:cafe:dead:beef::/64` | LAN-side IPv6 prefix for NPTv6 |
| `WAN_IF` | `enp2s0` | Network interface facing the WAN |

**3. Edit the RA configuration**

```bash
sudo nano /opt/go-ra-server/config.yaml
sudo systemctl restart go-ra-server
```

### Day-to-day operations

```bash
# Status
sudo systemctl status go-ra-server

# Start / stop / restart
sudo systemctl start   go-ra-server
sudo systemctl stop    go-ra-server
sudo systemctl restart go-ra-server

# Live log stream
sudo journalctl -u go-ra-server -f

# Logs since last boot
sudo journalctl -u go-ra-server -b

# Check RA daemon status (interfaces, TX counts)
gora status
```

### Updating

```bash
# 1. Rebuild the binary
cd go-ra && go build -o gorad ./cmd/gorad && cd ..

# 2. Deploy and restart
sudo install -m 755 go-ra/gorad /opt/go-ra-server/gorad
sudo systemctl restart go-ra-server
```

### Changing the installation path

```bash
sudo sed -i 's|/opt/go-ra-server|/your/path|g' \
  systemd/go-ra-server.service
```

Then re-run `setup.sh` with `INSTALL_DIR=/your/path`.

## gRPC API

`gorad` exposes a gRPC server (default `localhost:50051`). The RA Controller uses this to push RA policy at runtime without restarting the daemon.

| RPC | Description |
|-----|-------------|
| `GetStatus` | Returns runtime status of all RA instances (state, TX counts) |
| `ListInterfaces` | Returns the full `InterfaceConfig` list currently running (added in this fork; used by the controller's RA Interface Detail panel) |
| `AddInterface` | Adds a new RA instance on an interface |
| `UpdateInterface` | Updates an existing RA instance |
| `DeleteInterface` | Removes an RA instance (sends goodbye RA if configured, unless `skip_goodbye` is set) |
| `SendRA` | Sends one RA without installing a configuration; used by the controller to withdraw routes or a default router from specific hosts |

```bash
# Quick check with grpcurl
grpcurl -plaintext localhost:50051 gora.v1.GoRAService/GetStatus
```

See [`go-ra/api/gora/v1/gora.proto`](go-ra/api/gora/v1/gora.proto) for the full API definition.
