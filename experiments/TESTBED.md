# Running the experiments on real machines

How to run the experiments on the four machines of the paper (two site-exit
routers, the RA controller, the end-host emulator) instead of on one machine.

[English](TESTBED.md) / [日本語](TESTBED_ja.md)

> **Status: not tested.** Single mode (everything on one machine) has been
> run end to end. Testbed mode was written and reviewed without access to
> such machines and has never been executed. Follow the order below, read
> what `./exp check` prints, and expect to fix small things on the first run.
> If a result matters, cross-check it against single mode.

## 1. What runs where

```
 operator's machine ── SSH ──┬─ controller   RA controller (API on port 8080)
   ./exp …                   ├─ router-a     gorad, NPTv6, upstream A
                             ├─ router-b     gorad, NPTv6, upstream B
                             └─ emulator     bridge + N host namespaces

 site LAN:   emulator uplink ── switch ── router-a lan_if, router-b lan_if
 management: every machine reachable by SSH on an interface that is NOT
             a router's lan_if / wan_if and NOT the emulator's uplink
```

`./exp` runs on the operator's machine (it may be the controller machine
itself). It reaches the others only by SSH and runs everything there with
`sudo`. Nothing is installed permanently: processes are started in the
background and every file goes to `workdir` (`/var/tmp/polaris-exp`).

## 2. Requirements

| Where | Requirement |
|-------|-------------|
| Operator's machine | Python 3.11 or newer, `ssh`, `tar`, a checkout of this repository with the go-ra submodule (`git submodule update --init`) |
| Every target machine | Linux with `apt` (Ubuntu 24.04 is what the scripts were written for); SSH **key** login from the operator's machine; `sudo` without a password for that user |
| Routers and controller | Internet access for the first build: Go downloads a toolchain and modules (the controller also needs `gcc`; `setup` installs both with `apt`) |
| Every target machine | Synchronised clocks (chrony or NTP). `T_withdraw` subtracts a router timestamp from an emulator timestamp |
| Routers | A **management interface** for SSH and for the controller's gRPC (`mgmt_addr`) that is neither `lan_if` nor `wan_if`. The failover experiment takes those two down |
| Routers | NPTv6 from the site prefix to the ISP prefix, and whatever makes the ISP prefix reach the router (ND proxy, routing). Either you have it already (`manage_nptv6 = false`) or `setup` installs the two NPTv6 rules (`manage_nptv6 = true`); the ND proxy is never configured by `setup` |
| Emulator | A NIC connected only to the site LAN (`emulator_uplink`). It is attached to a bridge and loses its own IP configuration |
| Emulator | A kernel with `CONFIG_IPV6_ROUTE_INFO` (Ubuntu has it), otherwise the RIO routes are installed by `rio-shim` and are not evidence of kernel behaviour |

Passwordless `sudo` for one user, if you do not have it yet (on each target):

```bash
echo "$USER ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/polaris-exp
```

Remove that file when the experiments are done.

## 3. Before the first run

### 3.1 Stop what would interfere

`setup` starts its own `gorad` on each router (gRPC on port 50051) and its own
controller (port 8080). Anything already doing those jobs has to stop first:

```bash
# on each router
sudo systemctl stop gorad radvd 2>/dev/null     # whatever sends RAs on the site LAN
# on the controller machine
sudo systemctl stop ra-controller 2>/dev/null   # or: docker compose down
```

The `gorad` that `setup` starts advertises one baseline RA (the site prefix,
preference medium, Router Lifetime as configured) and nothing else. Options
your own RA configuration carries (RDNSS, MTU, other prefixes) are absent
during the experiments.

### 3.2 Write the configuration

```bash
cd experiments
cp config/testbed.example.toml config/testbed.toml
```

| Setting | What to put |
|---------|-------------|
| `machines.controller`, `machines.emulator`, `router.ssh` | SSH targets (`user@host`), reachable on the management network |
| `machines.emulator_uplink` | The emulator NIC on the site LAN. **Never the interface your SSH session uses** |
| `site.hosts`, `policy.g1_hosts` | 200 and 50 in the paper |
| `site.lan_prefix` | The site prefix the routers advertise to the hosts |
| `site.manage_nptv6` | `false` if the routers already translate; `true` to let `setup` add the rules (they are removed by `teardown`) |
| `router.isp_prefix` | The /64 of that router's ISP. Used to recognise the ISP from the address a server saw |
| `router.mgmt_addr` | The address on which the **controller** reaches that router (gRPC, port 50051) |
| `router.lan_if`, `router.wan_if` | Interface names on the router |
| `router.fail_cmd`, `router.restore_cmd` | How to break and restore that router's upstream (default: `ip link set <wan_if> down` / `up`). Use these to act on a switch port or a remote device instead |
| `upstream.kind` | `real` for real ISPs; `emulated` if your testbed has its own ISP and server emulation |
| `upstream.urls.default` | With real ISPs: a URL fetched with `curl -6` that answers with the client's address (for example `https://ifconfig.co/ip`). It tells which ISP a host left through |
| `upstream.urls.d1` | Optional: a URL whose addresses lie inside `policy.d1`, or a list with one URL per D1 prefix. It is judged by packet counts only |
| `policy.default_dst` | An address **outside** D1 that answers ping. The failover and transition experiments ping it continuously |
| `policy.d1_dst` | An address **inside** D1 that answers ping. Needed by the transition experiment; the default (first address of the first prefix) does not answer on the Internet |
| `experiments.failover.testbed_confirmed` | Leave `false` until Section 5 is done |

`policy.d1` defaults to the seven Microsoft Teams prefixes of the paper. If no
address inside them answers ping from your site, either skip the transition
experiment or set `policy.d1` to prefixes that contain a host you can ping,
and say so in the paper.

### 3.3 Check, without changing anything

```bash
./exp -c config/testbed.toml check
```

It prints one line per finding and changes nothing on any machine:

| Check | Meaning of a `FAIL` |
|-------|---------------------|
| access | SSH or `sudo` does not work |
| clock | (warning) the clock differs from the operator's machine by more than the measurement can explain |
| packages, build | Something is missing and cannot be installed, or the Go module proxy is unreachable |
| lan_if, wan_if, baseline address | The interface does not exist, is down, or has no link-local address |
| port | Something already listens on the gRPC port (router) or the API port (controller): see 3.1 |
| management path | The SSH session reaches the router through `lan_if` or `wan_if`. Only IPv6 sessions can be checked; with IPv4, check by hand |
| uplink | The emulator's uplink does not exist, or the SSH session uses it |
| reach Router(x) | The controller cannot ping the router's `mgmt_addr` |

Warnings to take seriously: `RA daemons` (another RA daemon is running),
`upstream route` (the router learns its default route from an RA and would
lose it when forwarding is enabled; set `accept_ra=2` on `wan_if`), `NPTv6`
(no translation rule found although `manage_nptv6` is false), `RIO support`,
`policy.d1_dst`.

Fix every `FAIL` before going on.

## 4. Set up and run

```bash
./exp -c config/testbed.toml setup          # first run: several minutes (builds)
./exp -c config/testbed.toml status
./exp -c config/testbed.toml run verify     # start with the cheapest experiment
./exp -c config/testbed.toml run forwarding load spread overwrite transition
```

### What `setup` changes on each machine

| Machine | Change | Undone by `teardown` |
|---------|--------|----------------------|
| all | Creates `workdir` (mode 0700, root); installs missing packages (`iproute2`, `tcpdump`, `curl`, `python3`, `iputils-ping`; routers: `iptables`; builders: `golang-go`, `gcc`) | directory stays; packages stay |
| routers | Builds `gorad`; sets `net.ipv6.conf.all.forwarding=1`; adds the policy link-local address (`policy_ll`, default `fe80::face:a` / `fe80::face:b`) to `lan_if`; with `manage_nptv6`, adds two `ip6tables -t mangle` chains (`PX_POST`, `PX_PRE`); starts `gorad` listening on `[::]:50051` | address and chains removed, `gorad` stopped; **forwarding stays 1** |
| controller | Builds and starts the controller on port 8080 with a fresh database in `workdir`, periodic reconcile off | stopped |
| emulator | Raises the neighbour table limits (`net.ipv6.neigh.default.gc_thresh1/2/3`); creates the bridge `br-px`, disables IPv6 on the bridge and on the uplink, attaches the uplink; creates the host namespaces `pxh001…` with fixed MAC addresses `02:00:00:00:xx:xx` | namespaces and bridge removed; **the uplink is left detached and without IPv6**, limits stay |

The hosts get link-local addresses `fe80::ff:fe00:<n>` and learn everything
else from the RAs, like real hosts. On each host only one thing is set:
`accept_ra_rt_info_max_plen=128`, without which Linux ignores Route
Information Options.

### Notes per experiment

| Experiment | On real machines |
|------------|------------------|
| `verify` | Reads routing tables only. Independent of the upstream |
| `forwarding` | With real ISPs, each host runs `curl -6` against `upstream.urls`. The ISP is recognised from the address in the answer (`isp_prefix`), and the packets are counted on both routers' `wan_if` |
| `load`, `spread` | Capture on the router's `lan_if` and on the emulator's bridge. On real machines the two `spread` columns differ by the network between them — this is the measurement reviewer comment 5 asks for |
| `overwrite` | Restarts `gorad` with a shared source address, then restores it |
| `transition` | Pings `policy.default_dst` and `policy.d1_dst` from every host while the policy changes. Both must answer |
| `failover` | See Section 5 |

Each run writes `meta.json` with the kernel and OS of every machine and its
clock offset; use it for the table of software specifications.

## 5. The failover experiment

It is the only experiment that breaks things on purpose. On Router(b) — the
second `[[router]]` — it

* breaks the upstream with `fail_cmd` (`withdraw`, `daemon_crash`, `withdraw_lost`),
* kills `gorad` and sets **`lan_if` and `wan_if` down** (`power_loss`),
* inserts an `ip6tables` rule that drops outgoing RAs (`withdraw_lost`),

and undoes each after the observation window.

Before enabling it:

1. Confirm that SSH to Router(b) and `mgmt_addr` do **not** use `lan_if` or
   `wan_if`. `./exp check` verifies this for IPv6 sessions only.
2. Have console access to Router(b) (IPMI, serial, or someone nearby) for the
   first run.
3. If taking `wan_if` down on the router is not how you want to fail the
   upstream, set `fail_cmd` / `restore_cmd` (they run on the router, as root).
4. Set `experiments.failover.testbed_confirmed = true`.

Run one scenario with few trials first:

```toml
[experiments.failover]
testbed_confirmed = true
scenarios = ["withdraw"]
trials = 2
```

```bash
./exp -c config/testbed.toml run failover
```

Then restore the full list of scenarios and the trial counts.

`power_loss` here means "the RA daemon is killed and both interfaces go down
at once". The machine keeps running, so that it can be restored over SSH. To
cut the power for real, do it by hand and compare one measurement.

### If a run is interrupted

A run that is stopped with Ctrl+C tries to restore Router(b) before it exits. If the
operator's machine itself dies, Router(b) may be left with interfaces down
and the RA-dropping rule in place. To recover, on Router(b):

```bash
sudo ip link set <lan_if> up; sudo ip link set <wan_if> up
sudo ip6tables -D OUTPUT -o <lan_if> -p ipv6-icmp --icmpv6-type 134 -j DROP
```

then run `./exp -c config/testbed.toml setup` again; it starts from a clean
state.

## 6. Finish

```bash
./exp -c config/testbed.toml teardown
```

Then, by hand:

* start your own RA daemon and controller again (3.1);
* on the emulator, give the uplink NIC back its configuration
  (`sudo sysctl -w net.ipv6.conf.<uplink>.disable_ipv6=0`, then your usual
  network configuration);
* on the routers, set `net.ipv6.conf.all.forwarding` back if it was 0 before;
* remove `/etc/sudoers.d/polaris-exp` if you created it, and `workdir` if you
  do not need the build cache.

The results are in `experiments/results/` on the operator's machine.

## 7. Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| `setup` stops at `the controller cannot reach every router` | `mgmt_addr` is wrong, or a firewall blocks port 50051 from the controller |
| `N host(s) did not reach the 'none' state` | The hosts do not receive the routers' RAs: the uplink is on the wrong network, the switch filters RAs (RA Guard), or another RA daemon answers. Inspect one host on the emulator: `sudo ip -n pxh001 -6 route show` |
| Hosts have routes but `forwarding` fails for one router | The NPTv6 rules or the return path (ND proxy, ISP routing) of that router; the hosts leave with the site prefix and the ISP drops them |
| `T_withdraw` is negative or implausible | The clocks of the router and the emulator differ; see the offsets in `meta.json` |
| `neighbor table overflow` in `dmesg` on the emulator | The limits were not raised; `setup` does it when it can write the sysctl |
