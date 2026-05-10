# RA Controller

A web-based management UI and backend for the [go-ra (forked)](https://github.com/y-kzm/go-ra) IPv6 Router Advertisement daemon.
Configure routers, define routing rules, organise hosts into groups, and push the resulting policy to every router's go-ra agent over gRPC — all from a browser.

## Screenshots

| Clients | RA Policy Rules |
|---------|-----------------|
| ![Clients tab](../imgs/clients.png) | ![RA Policy tab](../imgs/rules.png) |

| Create policy group | Assign rules |
|---------------------|--------------|
| ![Create group](../imgs/groups.png) | ![Assign rules](../imgs/apply_rules.png) |

## Architecture

![Architecture](../imgs/architecture.png)

### Components

| Directory | Role |
|-----------|------|
| `backend/` | Go HTTP server — stores config in SQLite and pushes policy to routers |
| `frontend/` | Vite + React + TypeScript management UI |
| `modules/neighbor-collector-snmp/` | Polls routers via SNMP to discover IPv6 neighbors |
| `modules/neighbor-collector-static/` | Serves a static list of neighbors |
| `modules/endpoint-collector-static/` | Serves a static list of FQDN/IP endpoints |
| `modules/endpoint-collector-ms365/` | Fetches Microsoft 365 endpoints from the Microsoft API |

### Key concepts

- **Router**: A network device running a go-ra agent.  RA parameters are configured per router.
- **Rule**: A set of IPv6 prefixes / FQDNs to advertise via a specific nexthop router.
- **Group**: A named set of client hosts.  Groups are linked to rules to control which clients receive which routes.
- **Neighbor**: A discovered IPv6 host on the network.  Neighbors are the source of truth for group membership.

## REST API summary

Mounted under `/api`:

| Method | Path | Description |
|--------|------|-------------|
| GET    | `/routers` | List registered routers |
| POST   | `/routers` | Register a router (also used by `gora` self-registration) |
| GET    | `/routers/status` | Reachability + TX counters for all routers (parallel gRPC `GetStatus`) |
| GET    | `/routers/{id}/status` | Reachability for one router |
| GET    | `/routers/{id}/interfaces` | Live `InterfaceConfig` list for one router (gRPC `ListInterfaces`) |
| PUT    | `/routers/{id}` | Update router settings |
| DELETE | `/routers/{id}` | Delete a router |
| GET    | `/rules` | List rules |
| POST   | `/rules` | Create a rule (`{nexthop, entries: [{value: <prefix>}]}`) |
| DELETE | `/rules/{id}` | Delete a rule |
| GET    | `/groups` | List groups |
| POST   | `/groups` | Create a group (`{name, members: [<link-local>...]}`) |
| PUT    | `/groups/{id}/rules` | Replace rule assignments (`{rules: [<id>...]}`) |
| DELETE | `/groups/{id}` | Delete a group |
| GET    | `/neighbors` | List discovered neighbors |
| POST   | `/neighbors/refresh` | Force an immediate poll of all collectors |
| GET    | `/neighbor-sources` | List configured collector source labels |
| POST   | `/policy/apply` | Compile rules×groups into per-router gRPC pushes |
| GET    | `/fqdn/services` | List endpoint-collector services (FQDN-based rules) |
| GET    | `/fqdn/services/{name}/endpoints` | Resolve a service to IP prefixes |

## Quick start

```bash
# Start everything (backend + all enabled modules + frontend dev server)
./server.sh
```

The frontend dev server opens at `http://localhost:5173` by default.

## Configuration

All settings live in `param.yaml`:

```yaml
backend:
  port: 8080
  db_path: controller.db
  fetch_interval: 10        # seconds between neighbor polls
  neighbor_ifname: ""       # filter neighbors by interface name (empty = all)

modules:
  neighbor_collector_static:
    enabled: true
    port: 8083
    neighbors: []           # list of static neighbor entries

  neighbor_collector_snmp:
    enabled: true
    port: 8084
    poll_interval: 30
    snmp_targets:             # one entry per router; port/community are optional
      - host: "router1.example.com"
        port: 161
        community: "public"
      - host: "router2.example.com"
        port: 161
        community: "private"

  endpoint_collector_static:
    enabled: true
    port: 8082
    services: []            # list of static service entries

  endpoint_collector_ms365:
    enabled: true
    port: 8085
    db_path: ./m365_endpoints.db
    instance: Worldwide     # Worldwide | China | USGovDoD | USGovGCCHigh
```

#### Project structure

```
frontend/src/
  types.ts                    Shared TypeScript interfaces
  utils/
    api.ts                    getApiUrl() — resolves backend URL from VITE_API_URL
    format.ts                 formatDateTime() and neighborLedStatus() helpers
    yaml.ts                   Policy YAML generation and LCS-based diff
  providers/
    neighbor-rest.ts          REST client for /api/neighbors
    service-rest.ts           REST client for /api/fqdn/services
  components/
    RouterGrid.tsx            Router status cards (interface names are clickable)
    NeighborsTab.tsx          Clients tab (paginated neighbor table; shows source router)
    RulesTab.tsx              RA Policy tab
    GroupsTab.tsx             Policy Groups tab
    StatusLed.tsx             Reachability indicator
    NotificationStack.tsx     Toast notification overlay
    modals/
      ConfirmModal.tsx        Generic confirmation dialog
      FqdnModal.tsx           FQDN service picker
      NeighborModal.tsx       Neighbor picker with shift-select
      RouterConfigModal.tsx   Router add/edit/remove form
      RAInterfaceModal.tsx    Live RA InterfaceConfig viewer (PIO/RIO/Clients/DRP)
      YamlPreviewModal.tsx    Policy YAML and radvd config preview
      YamlDiffModal.tsx       Diff against last applied policy
  App.tsx                     State, API calls, layout
  main.tsx                    Entry point
```

## Neighbor refresh

The **Refresh** button in the Clients tab triggers a full end-to-end poll — not just a cache read:

1. Backend calls `POST /api/neighbors/refresh` on every collector **in parallel**
2. Each collector runs an immediate SNMP walk
3. Backend fetches the updated results and writes them to the DB
4. The UI shows the latest data

The button is disabled for 10 seconds after each refresh to prevent SNMP flooding.

Background polling (`fetch_interval` / `poll_interval`) continues independently.

## Systemd deployment

### Prerequisites

- Linux with systemd
- Go (for the initial binary build on first start)
- Python 3 + PyYAML (`pip3 install pyyaml`)

### Installation

**1. Create a dedicated system user**

```bash
sudo useradd -r -s /sbin/nologin -d /opt/ra-controller ra-controller
```

**2. Deploy the application**

```bash
sudo cp -r /path/to/ra-controller /opt/ra-controller
sudo chown -R ra-controller:ra-controller /opt/ra-controller
sudo chmod +x /opt/ra-controller/server.sh
```

**3. Edit param.yaml**

```bash
sudo -u ra-controller nano /opt/ra-controller/param.yaml
```

Configure `snmp_targets`, ports, and any other settings for your environment.

**4. (Optional) Create an environment override file**

```bash
sudo mkdir -p /etc/ra-controller
sudo cp /opt/ra-controller/systemd/env.example /etc/ra-controller/env
sudo nano /etc/ra-controller/env
```

Uncomment and set any values that differ from `param.yaml` defaults.

**5. (Optional) Prepare a log directory**

Only needed if you want file-based logs in addition to journald.

```bash
sudo mkdir -p /var/log/ra-controller
sudo chown ra-controller:ra-controller /var/log/ra-controller
```

Then set `LOG_DIR=/var/log/ra-controller` in `/etc/ra-controller/env`.

**6. Install and start the service**

```bash
sudo cp /opt/ra-controller/systemd/ra-controller.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ra-controller
```

### Day-to-day operations

```bash
# Status
sudo systemctl status ra-controller

# Start / stop / restart
sudo systemctl start   ra-controller
sudo systemctl stop    ra-controller
sudo systemctl restart ra-controller

# Live log stream
sudo journalctl -u ra-controller -f

# Logs since last boot
sudo journalctl -u ra-controller -b

# Filter by component (grep on the bracketed label)
sudo journalctl -u ra-controller -g '\[backend\]'
sudo journalctl -u ra-controller -g '\[neighbor_collector_snmp\]'

# Last 100 lines
sudo journalctl -u ra-controller -n 100
```

### Updating

```bash
# 1. Deploy new files
sudo rsync -a --chown=ra-controller:ra-controller /path/to/new-version/ /opt/ra-controller/

# 2. Restart the service (server.sh rebuilds Go binaries on start)
sudo systemctl restart ra-controller
```

### Changing the installation path

The service file defaults to `/opt/ra-controller`.  To use a different path, edit before installing:

```bash
sudo sed -i 's|/opt/ra-controller|/your/path|g' \
  /opt/ra-controller/systemd/ra-controller.service
```

### Log format

Each line from every component is prefixed with a timestamp and a bracketed component name:

```
YYYY-MM-DD HH:MM:SS [component-name] original log message
```

Example:

```
2026-05-08 15:32:01 [backend] starting go-ra client backend server on port 8080
2026-05-08 15:32:04 [neighbor_collector_snmp] snmp: refreshed 12 neighbor(s) from 2 target(s)
```

Under systemd, journald adds its own timestamp, so each line carries two timestamps — the outer one from journald and the inner one from `server.sh`.  The inner timestamp reflects when the component actually emitted the message.
