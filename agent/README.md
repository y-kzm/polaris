# go-ra-server Agent

An IPv6 Router Advertisement agent based on [go-ra](https://github.com/YutaroHayakawa/go-ra), deployed on each router node in the polaris system.

The agent runs `gorad` — the go-ra daemon — which sends IPv6 Router Advertisements (RA) to clients on the local network and exposes a gRPC API for runtime policy management. The [RA Controller](../controller) connects to this gRPC endpoint to push RA configuration dynamically. The agent also configures NPTv6 prefix translation and NDP proxy so that LAN-side prefixes are reachable from the WAN.

The `setup.sh` script handles the full one-time setup:
1. Applies IPv6 kernel parameters
2. Configures NPTv6 rules (LAN ↔ WAN prefix translation)
3. Starts NDP proxy (`ndppd`)
4. Builds and deploys the `gorad` binary
5. Installs and starts the systemd service
6. Optionally sets up the SNMP agent (`snmpd`) that the controller polls for the hosts of the site link

## Components

| Path | Role |
|------|------|
| `go-ra/` | go-ra source (git submodule — forked from YutaroHayakawa/go-ra) |
| `systemd/go-ra-server.service` | Systemd unit file |
| `setup.sh` | One-time setup script |
| `setup.conf.example` | Example of the settings file of `setup.sh` (copy to `setup.conf` and edit) |
| `setup.conf.paper.example` | The same, filled in for the configuration evaluated in the paper (baseline RA every 400 s with MTU and RDNSS, single RA source address) |

After `setup.sh` runs, `config.yaml` is deployed to `$INSTALL_DIR` (default `/opt/go-ra-server`).

## Prerequisites

- Linux with systemd
- Go (for building `gorad`)
- `ip6tables` with SNPT/DNPT target support (for NPTv6)
- `ndppd` (NDP proxy daemon)
- `snmpd` (net-snmp), only with `SNMP_COMMUNITY`; `setup.sh` installs it with `apt-get` if it is missing

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

`setup.sh` does this when `POLICY_LL` is set (together with `LAN_IF`), for
example in the settings file:

```
LAN_IF=eth1
POLICY_LL=fe80::face:1
```

It installs a systemd drop-in that assigns `POLICY_LL` to `LAN_IF` before
every start of the service, and it writes the interface's own link-local
address as `sourceAddress` of the baseline instance in `config.yaml`. (With
two link-local addresses, which one is used by default is not defined, so it
has to be named.) One manual step remains: in the RA controller, set the
router's **Policy RA source address** to `POLICY_LL`.

If you keep an existing `config.yaml`, add `sourceAddress` to the baseline
instance yourself; `setup.sh` warns when it is missing.

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

**2. Write the settings file and run setup**

```bash
cp setup.conf.example setup.conf
nano setup.conf                      # interfaces, prefixes, baseline RA, policy address
sudo ./setup.sh --print-config       # shows the settings and the config.yaml it would write; changes nothing
sudo ./setup.sh
```

`setup.sh` reads `./setup.conf` if it exists (`-c FILE` names another file).
Because the settings decide what root does, the file must belong to root or to
the user who calls `sudo`, and must not be writable by group or others
(`chmod go-w setup.conf`); otherwise `setup.sh` refuses it.
The file holds one `KEY=VALUE` per line. An environment variable of the same
name overrides the file, so a single value can still be set on the command
line: `sudo RA_INTERVAL_MS=400000 ./setup.sh`.
`WAN_IF`, `WAN_PREFIX` and `LAN_PREFIX` have no default; `setup.sh` stops before
changing anything if one of them is missing.

Keep the checkout in a directory that only you can write to: `setup.sh` and
its settings file are run and read as root.

| Setting | Default | Description |
|---------|---------|-------------|
| `WAN_IF` | (required) | Network interface facing the WAN |
| `WAN_PREFIX` | (required) | WAN-side IPv6 prefix for NPTv6: the /64 the ISP delegated to this router |
| `LAN_PREFIX` | (required) | LAN-side IPv6 prefix for NPTv6, advertised in the baseline RA |
| `LAN_IF` | (empty) | Interface towards the hosts. When set, `setup.sh` writes the baseline RA configuration |
| `RA_INTERVAL_MS` | `600000` | Interval of the unsolicited baseline RA (MaxRtrAdvInterval; gorad sends at this fixed interval) |
| `ROUTER_LIFETIME_S` | `1800` | Router Lifetime of the baseline RA |
| `RA_MTU` | `0` | MTU option (AdvLinkMTU); `0` omits it |
| `RDNSS` | (empty) | DNS servers, comma-separated; empty omits the option |
| `RDNSS_LIFETIME_S` | `1800` | Lifetime of the RDNSS option |
| `PREFIX_VALID_S` / `PREFIX_PREFERRED_S` | `2592000` / `604800` | Lifetimes of the prefix |
| `CUR_HOP_LIMIT` | `64` | Hop limit advertised to the hosts |
| `REACHABLE_TIME_MS` / `RETRANS_TIME_MS` | `0` / `0` | `0`: unspecified by this router |
| `POLICY_LL` | (empty) | Source address of the policy RAs (see above). Needs `LAN_IF` |
| `OVERWRITE_CONFIG` | `no` | `yes`: write `config.yaml` again; the previous file is kept as `config.yaml.bak-<date>` |
| `INSTALL_DIR` | `/opt/go-ra-server` | Installation directory |
| `GO` | (searched) | Path of the Go compiler. `sudo` does not keep your `PATH`, so `setup.sh` also looks in `/usr/local/go/bin` and in the Go directories of the invoking user; set `GO` if it is elsewhere. The build runs as the user who called `sudo`, not as root |
| `GRPC_ADDR` | `localhost:50051` | gRPC listen address for gorad |
| `SNMP_COMMUNITY` | (empty) | Community string for the RA controller. When set, `setup.sh` sets up `snmpd` (see below); empty leaves `snmpd` untouched |
| `SNMP_ALLOW_FROM` | (required with `SNMP_COMMUNITY`) | Address(es) or prefix(es) of the controller, comma-separated, IPv4 or IPv6 |
| `SNMP_LISTEN` | (required with `SNMP_COMMUNITY`) | `agentAddress` of `snmpd`: this router's management address, for example `udp6:[2001:db8:ffff::a]:161`. `udp:161,udp6:161` listens on every address, the WAN side included |

The defaults of the baseline RA are those of RFC 4861 (Section 6.2.1) and RFC 8106.

**3. The RA configuration**

With `LAN_IF` set, `setup.sh` writes `config.yaml` with the baseline RA only:
`LAN_PREFIX` with preference medium to all hosts, the MTU and RDNSS options if
configured, and no routes. The controller adds the policy configurations over
gRPC; they are not stored in the file.

An existing `config.yaml` is never changed unless `OVERWRITE_CONFIG=yes`.
Without `LAN_IF`, the sample `config.yaml` of go-ra is installed; it is an
example (interface `eth0`, documentation prefixes, a sample route) and has to
be edited before use:

```bash
sudo nano /opt/go-ra-server/config.yaml
sudo systemctl restart go-ra-server
```

**4. The SNMP agent (optional)**

The RA controller learns the hosts of the site link from each router's
neighbor cache, which its `neighbor-collector-snmp` module reads over SNMP v2c.
With `SNMP_COMMUNITY` and `SNMP_ALLOW_FROM` set, `setup.sh` installs `snmpd` if
it is missing, writes `/etc/snmp/snmpd.conf`, and enables and restarts the
service:

```
SNMP_COMMUNITY=<a string of your own>
SNMP_ALLOW_FROM=2001:db8:ffff::c           # the controller
SNMP_LISTEN=udp6:[2001:db8:ffff::a]:161    # this router's management address
```

The configuration it writes is read-only, answers only the sources in
`SNMP_ALLOW_FROM`, and exposes only the tables the controller reads
(`ipNetToPhysicalTable`, `ifName`, `ifDescr`, `inetCidrRouteTable`).
An `snmpd.conf` that `setup.sh` did not write is kept as
`snmpd.conf.bak-<date>`; the file is replaced as a whole, so other uses of
`snmpd` on the router have to be merged by hand.

The community string works like a password and is sent in clear text (SNMP
v2c): keep `setup.conf` private (`chmod 600 setup.conf`) and keep SNMP on the
management network. `setup.sh` refuses a community shorter than 12 characters
or a well-known word such as `public`; `openssl rand -hex 12` gives a suitable one. `--print-config` does not show it.
If a firewall is active on the router, allow UDP port 161 from the controller;
`setup.sh` does not change firewall rules.

Enter the same community in the controller (`param.yaml`, `snmp_targets`), in
quotes (`community: "..."`), with `ifname` set to `LAN_IF` and `self_ll` set to
the router's own link-local address on that interface; `setup.sh` prints both
at the end. The community may contain letters, digits and `. _ @ % + = -`.

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
