# What the experiments do

For each experiment: what it is meant to show, under which conditions, what
it does, what it measures and how it decides. For the commands see
[README.md](README.md); for real machines see [TESTBED.md](TESTBED.md).

[English](EXPERIMENTS.md) / [日本語](EXPERIMENTS_ja.md)

## Overview

| Experiment | What it shows | In the paper | Duration (200 hosts) |
|------------|---------------|--------------|----------------------|
| [`verify`](#verify) | Every host selects the router the policy designates | Section V-A | about 10 s |
| [`forwarding`](#forwarding) | Traffic really leaves through the designated ISP and returns | Reviewer 1, comment 4 | about 1 min |
| [`load`](#load) | The RA load is what Equations 3 and 4 say | Section V-B | about 2.5 min |
| [`spread`](#spread) | The spread of the unicast RA transmissions | Section V-C, comment 5 | about 7 min |
| [`overwrite`](#overwrite) | Without separate source addresses the RAs overwrite each other | Comment 1 | about 5 min |
| [`transition`](#transition) | Traffic is not interrupted while a policy changes or is removed | Comment 2 | about 1.5 min |
| [`failover`](#failover) | Switch-over time under cooperative and non-cooperative failures | Section V-D, comment 3 | about 50 min |

The durations were measured with the 200-host configuration in a container on
Docker Desktop (65 minutes in total).

## Common ground

### Environment

| Element | Description |
|---------|-------------|
| Routers | Two. Router(a) connects to ISP-A, Router(b) to ISP-B. Each translates the site prefix to its ISP's prefix (NPTv6) |
| Source addresses | Each router has two link-local addresses on the LAN: the baseline address (all hosts, preference medium) and the policy address (unicast to selected hosts) |
| Hosts | 200 by default, as network namespaces. Standard RA processing only; the one thing set is the sysctl that makes Linux accept Route Information Options |
| Upstream (single mode) | Two emulated ISPs that drop packets whose source is outside their prefix, and one server behind them that answers with the client address it saw |
| RA timing | As in the paper by default: baseline every 400 s, policy every 40 s, Router Lifetime 1800 s |

### Evaluation scenario (the paper's table)

| Group | Hosts | Rule |
|-------|-------|------|
| G1 | 1–50 | D1 (the seven Microsoft Teams prefixes) via Router(a) |
| G2 | 51–200 | default route (`::/0`) via Router(b) |

Algorithm 1 compiles it into two control request messages:
c1 = (Router(a), the seven routes of D1, medium, G1) and
c2 = (Router(b), no route, high, G2).

### How every experiment starts

No experiment inherits state from the one before it. Each begins by

1. restarting the routers' RA daemons with the RA timing it needs (if different);
2. applying an empty policy, which withdraws what is still advertised;
3. taking every host's interface down and up, which removes all RA-learned state;
4. waiting until every host is in the baseline state (only the medium entries
   of both routers) and has a usable global address;
5. applying its scenario, if any, and waiting until every host's routing table
   matches the policy exactly.

### Means of measurement

| Means | What it tells |
|-------|---------------|
| Host routing table (`ip -6 route show`, `ip -6 route get`) | RA-learned routes; first-hop router per destination |
| Host route-change events (`ip monitor route`) | When a route was added or removed |
| Continuous pings from the hosts | How long traffic was interrupted; a reply proves both directions |
| Captures on the routers' upstream interfaces | Through which router each host's packets left |
| Captures on a router's LAN side and on the hosts' link | When RAs were sent and arrived, their size and content |

NPTv6 rewrites the source address, so on the upstream side a packet cannot be
attributed to a host by its address. Pings therefore use a payload size that
is different for every host, and HTTP requests a TCP source port that is
different for every host.

---

## verify

**Shows**: after the policy is applied, every host selects the designated
first-hop router; destinations the policy does not mention are unaffected.

**Procedure**

1. Without a policy, read every host's routing table and its first hop toward
   each destination (before).
2. Apply the evaluation scenario and read the same (after).

There are eight destinations: one address inside each of the seven D1
prefixes, and `2001:db8::1`, which is outside D1.

**PASS if all hold**

- Every designated (host, destination) pair uses the designated router:
  1550 pairs with 200 hosts (50 G1 hosts × 7 D1 destinations + 150 G2 hosts × 8).
- Every host's routing table matches the policy **exactly**: each RIO route
  goes via the policy address of its router; a high-preference default router
  exists if and only if `::/0` is assigned; both routers' baseline (medium)
  entries are present. A stray route, or a second route for the same prefix,
  is a failure.

**Output**

| File | Content |
|------|---------|
| `first_hop_before.csv`, `first_hop_after.csv` | First-hop router per host and destination (the data of the paper's heat map) |
| `raw/routes_host001_*.txt`, `raw/routes_host051_*.txt` | Routing table of one host per group, before and after |
| `summary.md` | Hosts per router, by group and destination |

Before the policy each host chooses between two equal default routes on its
own, so a group is split. For G1 and the default destination nothing is
designated, so the split remains after the policy.

---

## forwarding

**Shows**: not only the routing table, but the traffic itself leaves through
the designated ISP and the answer comes back. This answers reviewer 1,
comment 4 (captures at both egress interfaces, bidirectional traffic tests).

**Procedure**

Before and after the policy, for one address inside each of the seven D1
prefixes and for the default destination (16 runs):

1. Start capturing the test connections on both routers' upstream interfaces.
2. Every host sends an HTTP request with `curl -6`, several hosts in parallel.
   Host i uses the TCP source port "base + i".
3. Stop the captures and count the packets per host, router and direction by
   port number.

The server answers with the client address it saw; the ISP prefix that
contains it tells which ISP the request came through.

**PASS if all hold**

- Every host receives an HTTP answer (traffic works in both directions).
- For every designated host, packets appear **only** on the designated
  router's upstream, and the server saw that router's ISP.
- Wherever packets of a host appear, both directions are seen.

**Output**

| File | Content |
|------|---------|
| `forwarding_hosts.csv` | Per host: designated router, success, ISP seen, packets out and in at each upstream |
| `raw/wan_<router>_<phase>_<target>.txt` | The upstream captures, as text |
| `summary.md` | Totals per group, and the packets dropped by the ISPs' ingress filters |

The table of `summary.md` can go into the paper as it is: per group, the ISP
seen and the packet counts at both upstreams. The zeros at the router that is
not designated are the point.

**Note**: with real ISPs, set `upstream.urls` to a URL that answers with the
client's address.

---

## load

**Shows**: the bandwidth taken by RAs is what the paper's Equation 3
(baseline only) and Equation 4 (with policy RAs) give.

**Conditions**: to finish quickly the RA intervals are shorter than in the
paper (default: baseline 40 s, policy 4 s, 120 s of capture). The equations
are inversely proportional to the intervals; the value for the paper's
intervals is computed from them.

**Procedure**

1. Apply the evaluation scenario.
2. Capture RAs on the hosts' link for the window.
3. Read the frame sizes of the baseline RA, of c1 and of c2.
4. Compute the measured bit rate from the bytes captured.

**PASS if** the measured bit rate including policy RAs agrees with Equation 4
for the same intervals (tolerance: one policy interval plus 5 %, because the
window is not a multiple of the intervals).

**Output**

| File | Content |
|------|---------|
| `load.json` | Frame sizes, RA counts, measured rate, equation values for the experiment's and the paper's intervals |
| `raw/ra_capture.txt` | The RA capture |

**Note**: the baseline RA carries the prefix, MTU and DNS-server options and
is 142 bytes, as in the paper. The policy RAs are smaller than the submitted
paper's (now: c1 190, c2 78 bytes; paper: 258, 90). The current go-ra encodes
a Route Information Option for a prefix of /64 or shorter in 16 bytes. The
numbers of Section V-B have to be updated from this experiment.

---

## spread

**Shows**: a policy RA is sent to its hosts one after another, so the first
and the last host receive it at different times. How large is that spread?
Following reviewer 1, comment 5, it is measured on the hosts' link as well as
at the router.

**Conditions**: one measurement per (hosts, RIOs) pair; eight by default.

| Hosts | RIOs |
|-------|------|
| 50 | 7 (the paper's c1) |
| 150 | 0 (the paper's c2) |
| 50, 100, 200 | 0 |
| 50 | 1, 16, 32 |

With zero RIOs the RA advertises a high-preference default router. The policy
RA interval is 2 s (the spread inside a round does not depend on it).

**Procedure** (per condition)

1. Apply an empty policy.
2. Start capturing RAs on Router(a)'s LAN side and on the hosts' link.
3. Apply a policy with one group of the given hosts, via Router(a).
4. Wait for 20 rounds.
5. Split the RAs from the policy address into rounds by the silence between
   them; keep only the rounds that contain every host.
6. The spread of a round is the time of its last RA minus that of its first.

**PASS if** at least one complete round was captured per condition. The size
of the spread is not judged.

**Output**

| File | Content |
|------|---------|
| `spread_rounds.csv` | Spread (ms) per condition, place of measurement and round |
| `summary.md` | Per condition: frame size, rounds, mean ± standard deviation and maximum, at the router and on the hosts' link |

**Note**: in single mode the router and the hosts share one machine, so the
two places differ only by a virtual link. The effect of a network and of host
processing shows only on real machines.

---

## overwrite

**Shows**: if the baseline RA and the policy RA leave from the same source
address, each overwrites the other on the hosts; with separate addresses this
does not happen. This answers reviewer 1, comment 1.

A host keeps one default-router entry per RA source address and overwrites
its preference and lifetime with every RA from that address (RFC 4861,
RFC 4191).

**Conditions**: two designs under the same conditions, with short intervals so
that overwrites happen many times (default: baseline 20 s, policy 8 s, 120 s).

| Design | Meaning |
|--------|---------|
| separate | The policy RA has its own source address (the current design) |
| shared | The policy RA uses the baseline's address (the submitted implementation) |

**Procedure** (per design)

1. Configure the routers for the design and apply the evaluation scenario.
2. For ten G2 hosts, record the default routers and their preferences every 0.25 s.
3. A sample counts as "policy in force" if a high-preference default router
   exists and all of them are addresses of Router(b).

**PASS if** the policy is in force in every sample of the separate design.
The shared design is reported for comparison and not judged.

**Output**

| File | Content |
|------|---------|
| `overwrite.json` | Totals per design and the time series of one host |
| `raw/samples_<design>.jsonl` | Every sample |
| `summary.md` | Per design: share of samples in force, number of high → medium changes, longest time not in force |

`summary.md` also prints one host's time series (`H` in force, `m` not):

```
separate  HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH
shared    HHHHHHHHHHHHHHHHHHHHmmmmHHHHHHHHHHHHHHHH
```

---

## transition

**Shows**: a running policy can be changed and removed without interrupting
traffic. The controller advertises the new state first and withdraws the old
one afterwards. This answers reviewer 1, comment 2.

**Procedure**: starting from the evaluation scenario, two phases in a row.

| Phase | Change |
|-------|--------|
| exchange | The routers of the two groups are swapped: G1 Router(a) → Router(b), G2 Router(b) → Router(a) |
| remove | The whole policy is removed; every host returns to the baseline |

In each phase:

1. Start capturing pings on both routers' upstream interfaces.
2. Every host pings the default destination and a destination inside D1 every
   0.1 s; route-change events are recorded.
3. After 3 s, apply the new policy.
4. After another 5 s, stop recording.

**PASS if all hold** (per phase and group)

- No ping is lost. Losses are counted from the numbers ping reports as sent
  and answered (the request in flight when recording stopped is not counted).
- In `exchange`, on every host the route via the new router appeared before
  the route via the old router disappeared. This is compared per destination;
  the smallest margin of each host is reported.
- In `exchange`, every host's packets leave through the new router afterwards.
- At the end every host's routing table matches the new policy exactly.

**Output**: the table of `summary.md` (pings lost, hosts with the right order,
median and minimum margin, time from the apply to the removal of the old
route) and the logs under `raw/`.

**Note**: with real ISPs the destination inside D1 (`policy.d1_dst`) has to
answer ping.

---

## failover

**Shows**: how long hosts take to switch to Router(a) when Router(b) fails.
Following reviewer 1, comment 3, it covers non-cooperative failures as well
as the cooperative withdrawal, splits the time into intervals, and reports
repetitions and unsuccessful trials.

### Four failures

| Scenario | What is done | What ends the outage | Trials (default) | Window |
|----------|--------------|----------------------|------------------|--------|
| `withdraw` | The upstream link is broken. A watchdog notices and stops the RA daemon, which sends final RAs with lifetime 0 | The withdrawal RAs | 10 | 6 s |
| `power_loss` | The RA daemon is killed and, at the same instant, the LAN and upstream interfaces are set down | Neighbor unreachability detection on the hosts | 10 | 75 s |
| `daemon_crash` | The RA daemon is killed; 5 s later the upstream link is broken | Expiry of the Router Lifetime | 5 | lifetime + 15 s |
| `withdraw_lost` | As `withdraw`, but the router drops every RA it sends | Expiry of the Router Lifetime | 5 | lifetime + 15 s |

A trial of the last two lasts as long as the Router Lifetime, so a short one
is used (default: Router Lifetime 60 s, RAs every 20 s). The outage scales
with the lifetime.

The watchdog is a program of these scripts (`tools/watchdog.py`). It detects a
lost carrier on the upstream interface and signals the RA daemon to stop.

### One trial

1. Start from a clean site with the evaluation scenario applied.
2. Start captures: pings on both upstreams, RAs on the hosts' link.
3. Every host pings the default destination every 0.1 s; route-change events
   are recorded.
4. After 3 s, inject the fault.
5. Wait for the window.
6. Stop recording and analyse.
7. Undo the fault, restart the RA daemon, and rebuild the state of step 1.

### What is measured

The hosts considered are those whose packets left through Router(b) just
before the fault: all of G2, and the G1 hosts that had chosen Router(b) for
the default destination on their own.

| Value | From | To | Clock |
|-------|------|----|-------|
| `T_detect` | fault injected | watchdog notices | router |
| `T_withdraw` | watchdog notices | last withdrawal RA for this host seen on the hosts' link | router → host side |
| `T_host` | that RA on the link | Router(b)'s default route removed from the host | host side |
| `outage` | — | longest silence of the host's pings | host side |

- A host needs one or two withdrawal RAs: the one from the baseline address
  (multicast) always, and the one from the policy address (unicast) if it has
  a policy entry. `T_withdraw` ends when all of them have appeared.
- The resolution of `outage` is the ping interval (0.1 s). A value below it
  means that no ping was lost.
- A host whose pings do not resume inside the window counts as "not
  recovered"; so does one whose replies stop well before the window ends.
- In the non-cooperative scenarios there is no withdrawal RA, so `T_withdraw`
  and `T_host` are empty.

### Verdict

- `withdraw`: FAIL if a host did not recover or a trial failed.
- `withdraw_lost`: FAIL if a withdrawal RA was seen on the hosts' link
  although it should have been dropped (the trial would have measured
  `withdraw`).
- The other scenarios are reported, not judged.

### Output

| File | Content |
|------|---------|
| `failover_hosts.csv` | The four values and "recovered" per scenario, trial and host |
| `timeline_<scenario>.csv` | For the first trial: hosts using each router at 0.3 s ticks (the data of the paper's figure) |
| `raw/<scenario>_<trial>/` | Ping logs, route-change events, captures, watchdog log |
| `summary.md` | Per scenario: trials, measurements, not recovered, median (min–max) of each value, notes |

### Reading the results

- In `withdraw` the outage is the sum of detection, withdrawal and host time.
- In `power_loss` it lasts until the host notices that Router(b) is
  unreachable. That depends on how long the neighbor entry is still
  considered reachable, so it varies widely between trials.
- In `daemon_crash` and `withdraw_lost` it lasts until the Router Lifetime
  runs out: "lifetime minus the time since the last RA", bounded by the
  lifetime.

---

## Where each result goes in the paper

| Place in the paper | Experiment | What to use |
|--------------------|------------|-------------|
| Section V-A, routing-table figure | `verify` | `raw/routes_host*.txt` |
| Section V-A, first-hop figure | `verify` | `first_hop_before.csv`, `first_hop_after.csv` |
| Addition to Section V-A (comment 4) | `forwarding` | the table of `summary.md` |
| Section V-B | `load` | `load.json` (frame sizes and equation values) |
| Section V-C, spread figure (comment 5) | `spread` | `spread_rounds.csv` |
| Addition to Section III (comment 1) | `overwrite` | the table and time series of `summary.md` |
| Addition to Section III (comment 2) | `transition` | the table of `summary.md` |
| Section V-D, failover figure (comment 3) | `failover` | `timeline_withdraw.csv`, the table of `summary.md` |
| Table of software specifications | any | `meta.json` (kernel and OS of every machine) |

No figure is produced; draw them from the CSV files.

## Before using a result

- **Check that the kernel processed the RIOs.** If `kernel_rio` in `meta.json`
  is `no`, the RIO routes were installed by `rio-shim`. That shows that the
  routers sent the right RAs, not how a kernel handles them. On Ubuntu it is
  `yes`.
- **Times from single mode are not times of real machines.** All nodes share
  one machine: network delay is close to zero and they compete for the CPU.
  Take the times of `spread` and `failover` for the paper from real machines.
- **Some experiments shorten the intervals**: `load`, `spread`, `overwrite`
  and the last two `failover` scenarios. The values used are in each
  `meta.json`; state them in the paper.
