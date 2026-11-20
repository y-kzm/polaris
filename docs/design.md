# Polaris Controller — Design

This document defines how the controller is divided into components, what
each component may know about the others, and how the whole system is
started. It is the reference for the implementation; where code and document
disagree, one of them is a bug.

[English](design.md) / [日本語](design_ja.md)

## 1. Requirements

| # | Requirement | What it means here |
|---|-------------|--------------------|
| R1 | **Loose coupling** | Components talk only through the four contracts of Section 3. No component reads another's database, files or configuration, and none duplicates another's logic. Any component can be replaced, restarted or absent without the others being changed. |
| R2 | **Simple start-up** | One configuration file and one command start the whole controller. No toolchain is needed to run it. |
| R3 | **The screen tells the truth** | What the UI shows about a policy is computed by the backend from the routers' real state, never reconstructed in the browser. |
| R4 | **No orphaned state** | Every change in the controller's database that affects hosts is followed by the matching change on the routers, or is refused. |
| R5 | **Degrade, do not corrupt** | A source that fails keeps its last good data for a bounded time. A failure never turns into "no hosts" or "no prefixes". |

Non-goals: authentication of administrators, TLS between controller and
agents, high availability of the controller. Section 8 states what is
assumed instead.

## 2. Components

```
                 ┌────────────────────────── controller host ──────────────────────────┐
  browser ─HTTP─▶│ backend ── serves the UI and the REST API (C4)                       │
                 │    │  │                                                             │
                 │    │  ├─HTTP (C2)─▶ neighbor sources   (snmp, static, …)             │
                 │    │  └─HTTP (C3)─▶ endpoint sources   (ms365, static, …)            │
                 └────┼─────────────────────────────────────────────────────────────────┘
                      └─gRPC (C1)─▶ agent (go-ra) on every site-exit router ─RA─▶ hosts
```

| Component | Responsibility | Knows about | Does not know |
|-----------|----------------|-------------|---------------|
| **Agent** (go-ra, on each router) | Sends RAs for the configurations it is given | Nothing outside its router | Policies, groups, other routers |
| **Backend** | Stores the policy; compiles it (Algorithm 1); reconciles every router; serves the UI | Contracts C1–C3 | How a source obtains its data (SNMP, Microsoft, a file) |
| **Neighbor source** (module) | Answers "which hosts are on the site link" | Its own data source and its own filters | Policies, routers' RA state, the backend |
| **Endpoint source** (module) | Answers "which prefixes belong to service X" | Its own data source | Policies, the backend |
| **Frontend** | Renders the REST API | Contract C4 | Algorithm 1, RA configurations, the sources |

Consequences of this table:

* The frontend holds **no** policy logic. Previews and differences come from
  `GET /api/policy/plan`.
* Filtering neighbors by interface is the **source's** job (it knows the
  routers' interface names). The backend passes no filter.
* A module is any HTTP server that implements C2 or C3. Adding one needs a
  URL in the configuration, not a change to the backend.

## 3. Contracts

### C1 — Agent (gRPC, `gora.v1.GoRAService`)

Defined in `controller/backend/api/gora/v1/gora.proto`. The backend uses
`ListInterfaces`, `AddInterface`, `UpdateInterface`, `DeleteInterface`
(`skip_goodbye`), `SendRA` and `GetStatus`. The backend owns only the
configurations whose `description` is `polaris-policy`; it never changes any
other.

### C2 — Neighbor source (HTTP)

| Method and path | Response |
|-----------------|----------|
| `GET /api/neighbors` | `200` with a JSON array (below). Only hosts that can currently receive an RA. |
| `POST /api/neighbors/refresh` | Optional. `204` after the source refreshed its data. A source without it answers `404` or `405`. |
| `GET /health` | `200` if the process is up. |

```json
[{ "ip": "fe80::1", "lladdr": "02:00:00:00:00:01", "ifindex": 3, "ifname": "eth1",
   "state": "REACHABLE", "is_router": 0, "source": "router-a", "updated_at": "2026-11-20T01:02:03Z" }]
```

`ip` is required; the backend keeps only link-local unicast addresses. All
other fields are informational. A source must answer from memory: the backend
waits at most 5 s.

### C3 — Endpoint source (HTTP)

| Method and path | Response |
|-----------------|----------|
| `GET /api/fqdn/services` | `200` with `[{ "name": "Teams", "description": "…" }]` |
| `GET /api/fqdn/services/{name}/endpoints` | `200` with `{ "fqdns": [], "ipv4_prefixes": [], "ipv6_prefixes": ["2603:1027::/48"] }`; `404` if the source does not know the service |
| `GET /health` | `200` if the process is up. |

The backend uses only `ipv6_prefixes`. A source must not answer `200` with an
empty list when it merely failed to load its data; it answers `503`.

### C4 — Controller REST API

All paths are under `/api`. Errors are `{"error": "<message>"}` with a 4xx or
5xx status; the message is written for the administrator.

| Method and path | Purpose |
|-----------------|---------|
| `GET /health` | Liveness |
| `GET /routers`, `POST /routers`, `PUT /routers/{id}` | List, register, edit |
| `DELETE /routers/{id}[?force=1]` | Decommission (Section 5.4) |
| `GET /routers/status`, `GET /routers/{id}/status`, `GET /routers/{id}/interfaces` | What the agents report |
| `GET /rules`, `POST /rules`, `PUT /rules/{id}`, `DELETE /rules/{id}` | Rules; deleting one also removes it from every group |
| `GET /groups`, `POST /groups`, `PUT /groups/{id}`, `PUT /groups/{id}/rules`, `DELETE /groups/{id}` | Groups |
| `GET /neighbors`, `POST /neighbors/refresh` | Hosts seen by the neighbor sources |
| `GET /fqdn/services`, `GET /fqdn/services/{name}/endpoints` | Services known to the endpoint sources |
| `GET /sources` | Health of every source (Section 6) |
| `GET /policy/plan` | What applying the current draft would do (Section 5.2) |
| `POST /policy/apply[?dry_run=1]` | Apply the draft (Section 5.3) |

Validation happens here, once, and stores canonical forms:

| Field | Rule |
|-------|------|
| Router `name`, `address` | Required, each unique: one agent is one router. Changing the address moves the rules (draft and applied) to the new address. |
| Router `policy_source_address` | Empty, or a link-local unicast address |
| Router `status` | `active` or `inactive` |
| Rule `nexthop` | The address or ID of a registered router |
| Rule entry | `{"type":"ipv6","value":"<prefix>"}` — an IPv6 prefix or address, stored masked; or `{"type":"service","value":"<name>"}` — resolved at apply time (Section 5.5) |
| Group `members` | Link-local unicast addresses, canonical, without duplicates |
| Group `rules` | IDs of existing rules |

## 4. Data model

```
Router  id, name, address, interface, policy_source_address, status, RA parameters
Rule    id, comment, nexthop → Router, entries[]            ("D → n" of the paper)
Group   id, name, members[], rules[] → Rule                 ("G" and "f(G)")
AppliedPolicy   the rules and groups as of the last successful apply, and when
ServiceCache    service name → IPv6 prefixes of its last successful resolution
```

`status` of a router: an **inactive** router takes no part in the policy.
Rules that point to it are skipped with a warning, its policy state is
withdrawn at the next apply if it can be reached, and its being unreachable
does not hold back the other routers. It is the switch an administrator uses
for a router that is down or under maintenance.

## 5. Policy lifecycle

```
 edit (draft in the database) ──▶ plan ──▶ apply ──▶ applied snapshot ──▶ periodic reconcile
                                                             ▲                    │
                                                             └────────────────────┘
```

### 5.1 Draft

Editing rules and groups changes only the database. Nothing reaches a router
until an apply.

The router registry is infrastructure, not policy: a change to a router
(its status, addresses or RA parameters) takes effect at the next apply **or**
the next periodic reconcile, whichever comes first.

### 5.2 Plan — `GET /api/policy/plan`

Computes, without changing anything, what an apply would do:

```json
{
  "pending": true,
  "applied_at": "2026-11-20T01:02:03Z",
  "warnings": ["host fe80::1: 2001:db8::/48 is assigned to both Router(a) and Router(b); …"],
  "routers": [{
    "router": "Router(a)", "address": "fd00::a", "status": "active",
    "reachable": true, "error": "",
    "added": 1, "updated": 0, "deleted": 0, "unchanged": 0, "withdrawn": 0,
    "messages": [{
      "source_address": "fe80::face:a", "preference": "medium", "router_lifetime_seconds": 0,
      "routes": ["2603:1027::/48"], "clients": ["fe80::1", "fe80::2"]
    }]
  }]
}
```

`routers` has one entry per registered router, with the same fields as the
result of an apply. `messages` are the control request messages of Algorithm 1 for that router,
in the form they will be sent. `pending` is true when the draft differs from
the applied snapshot. This endpoint replaces the frontend's own YAML preview
and difference.

### 5.3 Apply — `POST /api/policy/apply`

1. Compile the draft (Algorithm 1), resolving service entries (5.5).
2. Read what every active router runs; compute the difference.
3. **Make**: add and update configurations on every router; send the new RAs.
4. **Break**, only if step 3 succeeded on every active router: send the
   withdrawal RAs, then delete the configurations. A router whose withdrawal
   fails keeps its configurations, so that the next apply or reconcile can
   compute the withdrawal again.
5. If every router succeeded, store the draft as the applied snapshot.
   Otherwise the snapshot stays as it was, and the periodic reconcile keeps
   enforcing the previous policy until an apply succeeds.

The response lists, per router, what was added, updated, deleted, left
unchanged and withdrawn, plus the warnings. With `dry_run=1` it stops after
step 2.

### 5.4 Decommission — `DELETE /api/routers/{id}`

A router leaves the controller only after its policy state has left the
hosts:

1. Refuse with `409` if a rule still points to the router — in the draft or
   in the applied policy; the message names the rules. (The hosts follow the
   applied policy: withdrawing a router it still uses would leave them
   without routes before another router advertises them.)
2. Delete the router's `polaris-policy` configurations and send the
   withdrawal RAs.
3. Delete the database row.

If the router cannot be reached, step 2 fails and the request is refused with
`409`, unless `force=1` is given; the response then says that the router may
still advertise the old policy until its lifetimes expire.

### 5.5 Service entries

A rule entry of type `service` names a service of an endpoint source. Its
prefixes are looked up at every compile, so a change at the source reaches
the routers at the next apply or periodic reconcile — as a make-before-break
transition like any other change. If no source answers, the prefixes of the
last successful lookup (ServiceCache) are used and a warning is returned. If
there has never been a successful lookup, the entry contributes nothing and a
warning is returned. If only some of several sources answer, the prefixes
last known from the others are kept, so that a flapping source does not make
routes come and go.

Entries of type `ipv6` that carry a `service` label (created by earlier
versions) stay literal prefixes.

### 5.6 Periodic reconcile

Every `reconcile_interval` (default 60 s; 0 disables it) the backend applies
the **applied snapshot** again. This

* restores the policy on a router whose agent was restarted,
* follows changes of service prefixes,
* completes a break phase that was skipped because a router was unreachable.

It uses the snapshot, not the draft, so half-finished edits are never applied
by the timer. Applies are serialised: a manual apply and the timer never
overlap.

## 6. Sources: failure handling

For every neighbor source the backend keeps the result of the last successful
fetch. A failed fetch leaves it in place. After `stale_after` (default five
fetch intervals) without success the source's hosts are dropped, and
`GET /api/sources` shows the source as failed:

```json
{ "neighbors": [{ "name": "neighbor-collector-snmp", "url": "http://127.0.0.1:8084/api/neighbors",
                  "ok": false, "error": "connection refused", "last_success": "…", "count": 187 }],
  "endpoints": [{ "url": "http://127.0.0.1:8085/api/fqdn/services", "ok": true, "error": "", "last_success": "…" }] }
```

Group members are stored addresses, not references to the neighbor table, so
losing a neighbor source never changes a policy; it only empties a pick list.

Inside the SNMP source the same rule applies per router: a router that fails
a poll keeps its last good neighbors for a bounded number of polls.

## 7. Start-up

### 7.1 One configuration file

`controller/param.yaml` is the only file an operator edits. It lists the
backend settings and, per module, whether it is enabled, its port and its own
settings. `server.sh` reads it and starts every enabled module and the
backend, passing each process only its own settings (environment variables).
No process reads `param.yaml` itself, so a module can also be run alone.

### 7.2 One command

| Environment | Command | Needs |
|-------------|---------|-------|
| Docker | `docker compose up -d` in `controller/` | Docker |
| No Docker | `./server.sh` in `controller/` | Go, Node.js (first run only, to build), Python 3 with PyYAML |

Both start the same `server.sh`. In Docker it runs inside one container that
already contains the binaries and the built UI.

* The backend serves the UI (`STATIC_DIR`), so there is **one port** (8080 by
  default) and no separate web server or development server.
* The container uses the host's network: the backend must reach the agents
  over IPv6 and the SNMP source must reach the routers, and neither should
  depend on Docker's IPv6 support.
* The database and the module's cache live in one data directory
  (a named volume in Docker).
* `server.sh` builds what is missing, waits for each process to listen (with
  a time limit), and exits when any process dies, so that Docker or systemd
  restarts the whole set.
* `./server.sh dev` additionally starts the Vite development server for work
  on the frontend.

### 7.3 Process layout and ports

| Process | Listens on (default) | Reachable from |
|---------|----------------------|----------------|
| backend (API and UI) | `:8080` | administrators |
| neighbor-collector-static | `127.0.0.1:8083` | the backend only |
| neighbor-collector-snmp | `127.0.0.1:8084` | the backend only |
| endpoint-collector-static | `127.0.0.1:8082` | the backend only |
| endpoint-collector-ms365 | `127.0.0.1:8085` | the backend only |

Modules listen on the loopback address unless `listen_host` in `param.yaml`
says otherwise (the lab, where each module is its own container, sets it).

## 8. Security assumptions

| Asset | Assumption or measure |
|-------|-----------------------|
| REST API and UI | No authentication. The backend port must be reachable only from the management network. A request that changes something is refused if it comes from another web site (its `Origin` is neither this server nor listed in `cors_origins`) or if its body is not `application/json`, so that a page the administrator happens to visit cannot trigger an apply. |
| Modules | Loopback only by default. The SNMP source accepts target registration over HTTP only when `allow_target_registration` is set (the lab uses it). |
| Agent gRPC | No TLS and no authentication. Port 50051 of the routers must be reachable only from the controller. Whoever reaches it can change the RAs of the site. |
| SNMP community strings | Kept in `param.yaml`; never logged. |

These are deployment constraints of a prototype, not properties to rely on in
production; the paper's security section discusses the threats.

## 9. What this design changes

| Before | After | Requirement |
|--------|-------|-------------|
| Deleting a router left its policy RAs running | Decommission withdraws first (5.4) | R4 |
| The Active/Inactive switch had no effect | Defined semantics (Section 4) | R3, R4 |
| Deleting a rule left its ID in groups | Removed in the same transaction | R4 |
| Preview and difference were built in the browser from the old model | `GET /api/policy/plan` (5.2) | R1, R3 |
| Service prefixes were copied into the rule when it was created | Resolved at apply time, with a cache (5.5) | R1, R5 |
| A restarted router stayed without policy until someone pressed Apply | Periodic reconcile of the applied snapshot (5.6) | R4 |
| One failed SNMP poll emptied the neighbor table | Last good data kept per source and per router (Section 6) | R5 |
| The MS365 source never loaded its data | Loads at start and periodically; validates what it stores | R5 |
| The backend passed one interface name to every source | Filtering belongs to the source | R1 |
| A source that was down looked like "no prefixes" | Errors are reported; `GET /api/sources` | R5 |
| Start-up needed Go, Node.js and PyYAML, rebuilt on every start, and ran the UI on a development server | One container, one port, prebuilt (Section 7) | R2 |
| Modules listened on every interface | Loopback by default | Section 8 |
