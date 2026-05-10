# Polaris Lab — Docker Compose Demo

A self-contained, ready-to-run Polaris deployment for evaluation and
demos.  Three site-exit routers, ten end hosts, the controller, and an
upstream web server, all in a single `docker compose up`.

[English](README.md) / [日本語](README_ja.md)

## Topology

Three Docker bridge networks, each modeling one segment of an IPv6
site multihoming deployment:

![topology](../imgs/topology.png)

| Segment | Subnet | Members |
|---------|--------|---------|
| `internal` | `fc00:cafe:dead:beef::/64` | host01..10, router1..3 (RA delivery) |
| `mgmt`     | `fd00::/64`                | controller, neighbor-collector, router1..3 (gRPC) |
| `server`   | `fd10::/64`                | web-server, router1..3 (forwarding upstream) |

Each router connects to all three networks.  Its **internal-segment
interface name** is detected dynamically by prefix
(`fc00:cafe:dead:beef::/64`) because Docker does not guarantee a
deterministic `eth0/eth1/eth2` assignment order.

## Quick start

```bash
# from repo root
git submodule update --init                # fetch agent/go-ra
docker compose -f laboratory/docker-compose.yaml up --build
```

Once everything is healthy (~30 s after the first build):

1. Open <http://localhost:3000>
2. Routers `Router(a/b/c)` are already registered (auto-self-registration
   on boot — `entrypoint-agent.sh` calls the controller's
   `POST /api/routers`).
3. Wait ~30 s for the first SNMP poll to populate the **Clients** tab
   with 10 hosts.
4. Apply a policy via the UI **or** the CLI script (see below).

> **Prerequisite**: the Docker daemon must have IPv6 enabled.
> Add `{"ipv6": true, "fixed-cidr-v6": "fd00:f::/64"}` to
> `/etc/docker/daemon.json` (or Docker Desktop → Settings → Docker Engine),
> then restart Docker.

## Demo workflow — RIO-based policy routing

The lab's verification scripts exercise **RIO-based** specific-prefix
routing only.

### Apply a policy (split hosts across two routers)

```bash
# Group 1: hosts 1-5  →  Router(a)  (RIO: fd10::/64)
sh laboratory/check/apply-policy.sh "Router(a)" 1-5

# Group 2: hosts 6-10 →  Router(b)
sh laboratory/check/apply-policy.sh "Router(b)" 6-10
```

Index `N` maps to the `polaris-lab-hostNN-1` container — e.g. `1-5`
means host01..host05, with each container's link-local resolved at
runtime.

Each invocation:
1. Resolves the requested indices to host01..10 link-locals.
2. Creates a Group with those hosts.
3. Creates a Rule (`nexthop = router.address`, `entries = [{value: fd10::/64}]`).
4. Assigns the rule to the group and `POST /api/policy/apply`.
5. Waits 5 s and runs `check-routing.sh`.

### Verify

```bash
sh laboratory/check/check-routing.sh
```

Reads each host's routing table and prints the `via` next-hop of the
`fd10::/64` RIO route:

```
HOST        NEXT HOP (RIO route)
host01      fe80::abcd:...     ← Router(a) link-local
host02      fe80::abcd:...     ← Router(a)
...
host06      fe80::ef01:...     ← Router(b)
```

`(no RIO route — not in any policy group)` means the unicast RA either
hasn't arrived yet, or the host isn't a member of any Group.

### CLI usage

```
sh laboratory/check/apply-policy.sh <router>  <hosts>

  <router>   "Router(a)" | "Router(b)" | "Router(c)"
             or the IPv6 address (e.g. fc00:cafe:dead:beef::ff01)
  <hosts>    "1-5"  range
             "1,3,5" list
             "all"  every host
```

---

## Demo workflow — through the Web UI

1. **Routers** card on the dashboard shows reachability.  Click an
   interface name (`eth0` / `eth1` / `eth2`) to inspect the running RA
   config (config #0 = baseline multicast, config #N≥1 = unicast policy
   with clients + RIO/DRP).
2. **Clients** tab — list of discovered hosts (link-local, source router,
   state).  Use multi-select.
3. **Policy Groups** tab — create a Group from selected clients.
4. **RA Policy** tab — create a Rule (destination `fd10::/64`, nexthop
   `Router(a)`).
5. Assign the Rule to the Group and click **Apply**.
6. Re-open the router's RA Interface modal — config #1 should show the
   selected clients.

---

## Components

| Service | Image source | Port | Role |
|---------|--------------|------|------|
| `web-server`         | `Dockerfile.webserver`           | 80 (internal) | nginx, lab "Internet" target |
| `router1/2/3`        | `Dockerfile.agent`               | 50051 (gRPC), 161 (snmpd) | go-ra + snmpd; self-registers with controller |
| `controller-backend` | `Dockerfile.backend`             | 8080 | Go HTTP API (chi router) |
| `controller-frontend`| `Dockerfile.frontend`            | 3000 → 80 | nginx serving the React build, proxies `/api` |
| `neighbor-collector` | `Dockerfile.neighbor-collector`  | 8083 | SNMP polling of routers (`ipNetToPhysicalTable`) |
| `host01..host10`     | `Dockerfile.host`                | —    | Alpine + `iputils` + `iproute2`, accepts RIO |

### Auto-discovery flow

1. Each router pings `ff02::1` on its internal interface at boot, then
   every 25 s — populates the kernel NDP cache with all hosts on the
   internal segment.
2. `snmpd` exposes the NDP cache via `ipNetToPhysicalTable`
   (RFC 4293 / OID `1.3.6.1.2.1.4.35`).
3. `neighbor-collector` polls all three routers in parallel and merges
   the results, filtering by:
   - the router's known internal interface (registered via
     `PUT /api/targets/{host}` from `entrypoint-agent.sh`)
   - link-local addresses only (`fe80::/10`)
4. `controller-backend` watches the collector and persists discovered
   neighbors in SQLite.

---

## RIO acceptance (essential!)

By default, the Linux kernel ignores RIO from RAs:
`net.ipv6.conf.<iface>.accept_ra_rt_info_max_plen = 0`.  Each lab host
sets it to 128 via Docker's `sysctls:` directive:

```yaml
sysctls:
  net.ipv6.conf.all.accept_ra_rt_info_max_plen: 128
  net.ipv6.conf.default.accept_ra_rt_info_max_plen: 128
```

Without this, the per-host unicast RA arrives but its RIO entries are
silently dropped — `ip -6 route show fd10::/64` returns nothing.

---

## Files

```
laboratory/
├── docker-compose.yaml          three-segment topology, 17 services
├── Dockerfile.agent             go-ra + snmpd builder (Alpine 3.21)
├── Dockerfile.backend           controller backend (Go 1.24, CGO for SQLite)
├── Dockerfile.frontend          React build + nginx
├── Dockerfile.host              minimal Alpine end-host
├── Dockerfile.neighbor-collector
├── Dockerfile.webserver         nginx with return routes via all routers
├── entrypoint-agent.sh          interface autodetect, snmpd, self-register, periodic ND ping
├── entrypoint-host.sh           sysctls (RA accept + RIO accept)
├── entrypoint-webserver.sh      add return routes to fc00:cafe:dead:beef::/64 via 3 routers
├── nginx.conf                   frontend SPA + /api proxy + cache headers
├── nginx-webserver.conf         upstream web server response
└── check/
    ├── apply-policy.sh          create group + rule + apply (RIO)
    └── check-routing.sh         show fd10::/64 RIO next-hop per host
```

## Troubleshooting

### Hosts show `(no RIO route ...)` after applying a policy

- Wait longer — the controller's `POST /api/policy/apply` triggers an
  immediate RA, but the host's NDP daemon may take a few RA intervals
  to install the RIO.
- Check `accept_ra_rt_info_max_plen`:
  ```bash
  docker exec polaris-lab-host01-1 \
    sysctl net.ipv6.conf.all.accept_ra_rt_info_max_plen
  # → 128
  ```
- Verify the router pushed the unicast config:
  ```bash
  curl -s http://172.20.2.2:8080/api/routers/<id>/interfaces | jq
  # → look for clients=[fe80::...] and routes=[{prefix: "fd10::/64", ...}]
  ```

### Neighbor list is empty

- Wait ~30 s for the first SNMP poll cycle.
- Inspect: `docker logs polaris-lab-neighbor-collector-1 | tail`
- Verify routers registered themselves:
  ```bash
  docker exec polaris-lab-controller-backend-1 \
    wget -qO- http://localhost:8080/api/routers
  ```
