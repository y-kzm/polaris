# Polaris Experiments

Scripts that build the experiment site and run every experiment of the paper,
from setup to result tables.

[English](README.md) / [日本語](README_ja.md)

## Quick start

On a Linux machine (Ubuntu 24.04 is what the scripts are written for), as root:

```bash
git submodule update --init          # fetch agent/go-ra (once)
cd experiments
sudo ./exp setup                     # packages, binaries, 2 routers, 2 ISPs, 200 hosts
sudo ./exp run all                   # every experiment; roughly an hour
sudo ./exp teardown                  # remove everything again
```

To try the scripts first, use the small configuration (20 hosts, about 20 minutes):

```bash
sudo ./exp -c config/quick.toml setup
sudo ./exp -c config/quick.toml run all
```

Each experiment prints its tables and writes them, with the raw data, to
`results/<experiment>_<timestamp>/`. `run` exits with a non-zero status if an
experiment reports `FAIL`.

## Commands

| Command | What it does |
|---------|--------------|
| `./exp check` | Look at every machine before `setup` touches it: access, clocks, interfaces, ports in use. Changes nothing |
| `./exp setup [--rebuild]` | Install missing packages, build `gorad` and the controller, create the network, start the services, wait until every host has received the baseline RAs |
| `./exp run <name>... \| all` | Run experiments (see below) |
| `./exp list` | List the experiments |
| `./exp status` | Show the routers, their addresses and the RA parameters in use |
| `./exp teardown` | Stop the processes and delete everything `setup` created |

`-c <file>` selects the configuration; the default is `config/single.toml`.

## Experiments

| Name | Paper | What it measures | Result |
|------|-------|------------------|--------|
| `verify` | Section V-A | Routing table and first-hop router of every host toward each D1 prefix and a default destination, before and after the policy | `first_hop_before.csv`, `first_hop_after.csv`, routing tables of one host per group |
| `forwarding` | Reviewer 1, comment 4 | `curl` from every host; the ISP the server saw; TCP packets at both routers' upstream interfaces, per host and direction | `forwarding_hosts.csv` |
| `load` | Section V-B | RA frame sizes; RA bit rate on the hosts' link compared with Equations 3 and 4 | `load.json` |
| `spread` | Section V-C, comment 5 | Time between the first and last unicast RA of a round, at the router and on the hosts' link | `spread_rounds.csv` |
| `overwrite` | Comment 1 | Default-router preference over time when the policy RA uses its own source address, and when it shares the baseline's | `overwrite.json` |
| `transition` | Comment 2 | Changing and removing a running policy under continuous pings: packets lost, order of route changes | tables in `summary.md` |
| `failover` | Section V-D, comment 3 | Switch-over under four kinds of failure, split into detection, withdrawal and host time | `failover_hosts.csv`, `timeline_<scenario>.csv` |

What each experiment does, measures and checks is described in
**[EXPERIMENTS.md](EXPERIMENTS.md)**.

Every run directory also holds `summary.md` (what was printed), `meta.json`
(configuration, kernel, git revisions, RA parameters) and `raw/` (captures and logs).

### `failover` in detail

Router(b) fails in four ways, each repeated `trials` times:

| Scenario | Failure | What ends the outage |
|----------|---------|----------------------|
| `withdraw` | The upstream link fails. A watchdog on the router notices and stops the RA daemon, which sends final RAs with lifetime 0 | The withdrawal RAs |
| `power_loss` | The RA daemon is killed and both interfaces go down at the same instant | Neighbor unreachability detection on the hosts |
| `daemon_crash` | The RA daemon is killed, then the upstream link fails | Expiry of the Router Lifetime |
| `withdraw_lost` | As `withdraw`, but the router drops its own outgoing RAs | Expiry of the Router Lifetime |

For every host whose traffic left through Router(b) just before the fault:

| Value | From | To | Clock |
|-------|------|----|-------|
| `T_detect` | fault injected | watchdog notices | router |
| `T_withdraw` | watchdog notices | last withdrawal RA for this host seen on the hosts' link | router → host machine |
| `T_host` | that RA on the link | default route removed from the host | host machine |
| `outage` | — | longest silence of the host's pings | host machine |

The table reports median, minimum and maximum, the number of trials, and the
hosts that did not recover inside the observation window. The last two
scenarios use a short Router Lifetime (`bound_lifetime_s`), because a trial
lasts as long as the lifetime.

The watchdog (`tools/watchdog.py`) is part of these scripts, not of Polaris.
It detects a lost carrier on the upstream interface (or, with `--mode ping`,
lost echo replies from the upstream gateway).

## Two modes

| | `single` (default) | `testbed` |
|---|---|---|
| Machines | One Linux machine; every node is a network namespace | The four machines of the paper, driven over SSH |
| Upstream | Two emulated ISPs with ingress filtering, and a server | Whatever the routers are connected to (`upstream.kind = "real"`) |
| Clocks | One | One per machine; synchronise them (chrony) |
| Configuration | `config/single.toml` | copy `config/testbed.example.toml` |
| Status | Tested | **Not tested** |

Single mode:

```
hosts pxh001… ─┐                       ┌─ px-ia (ISP-A) ─┐
               ├ br-lan ┬ px-ra ── wan ─┘                 ├ br-core ─ px-srv (server)
               │        └ px-rb ── wan ─── px-ib (ISP-B) ─┘
px-ctl (controller) ─ br-mgmt ─ px-ra, px-rb
```

Each router translates the site prefix to its ISP's prefix (NPTv6). Each ISP
drops packets whose source address is outside its prefix, so traffic that
leaves through the wrong router cannot be answered.

Testbed mode has its own step-by-step guide: **[TESTBED.md](TESTBED.md)**
(requirements, what to stop first, the configuration, `./exp check`, what
`setup` changes on each machine, and the precautions for the failover
experiment). It was written without access to such a testbed and has not
been run.

## Configuration

`config/single.toml` lists every setting with its default. The ones that
matter most:

| Setting | Default | Meaning |
|---------|---------|---------|
| `site.hosts` | 200 | Emulated hosts |
| `policy.g1_hosts` | 50 | Hosts 1…n form G1 (D1 via Router(a)); the rest form G2 (default via Router(b)) |
| `ra.baseline_interval_ms`, `ra.policy_interval_ms` | 400000, 40000 | RA intervals of the paper |
| `ra.router_lifetime_s` | 1800 | Router Lifetime |
| `experiments.failover.trials` | 10 | Repetitions of `withdraw` and `power_loss` |
| `experiments.failover.bound_trials` | 5 | Repetitions of `daemon_crash` and `withdraw_lost` |

Some experiments use shorter RA intervals than the paper so that they finish
in minutes (`load`, `spread`, `overwrite`, and the two lifetime-bound failover
scenarios). The values are in the configuration and in each `meta.json`.

## Kernel support for Route Information Options

Linux installs the routes of Route Information Options only if the kernel has
`CONFIG_IPV6_ROUTE_INFO` (Ubuntu does). `setup` checks this on the machine
that holds the hosts. If the option is missing — Docker Desktop is the common
case — each host runs `rio-shim`, a small program that installs those routes
the way the kernel would. Results obtained that way show that the routers
send the right RAs; they are not evidence of kernel behaviour. `meta.json`
records which case applied (`kernel_rio`).

## Trying it without a Linux machine

`dev/docker.sh` starts a privileged Ubuntu container with the repository
mounted (Docker Desktop on macOS or Windows):

```bash
dev/docker.sh ./exp -c config/quick.toml setup
dev/docker.sh ./exp -c config/quick.toml run all
docker rm -f polaris-exp-dev         # when done
```

Use this to check the scripts, not to produce results: the Docker Desktop
kernel needs `rio-shim` (see above). Set `GO_RA_DIR` to build from another
go-ra checkout.

## Files

```
experiments/
├── exp                      the command
├── config/                  single.toml, quick.toml, testbed.example.toml
├── expkit/                  Python modules (standard library only)
│   ├── config.py            configuration file
│   ├── node.py              run commands locally or over SSH, in a namespace
│   ├── topology.py          shell scripts that build the network
│   ├── provision.py         setup and teardown
│   ├── site.py              routers, controller API, host state
│   ├── policy.py            scenarios and the host state they must produce
│   ├── capture.py           tcpdump and its parsers
│   ├── measure.py           pings and route-change events of the hosts
│   ├── report.py            result files
│   └── experiments/         one module per experiment
├── tools/                   run on the machines: hostprobe.py, watchdog.py, webserver.py
├── dev/                     Docker helper
└── results/                 output (not tracked by git)
```

## Troubleshooting

- **`the site is not running`.** Run `./exp setup`.
- **`go-ra source not found`.** Run `git submodule update --init`, or set `go_ra_dir`.
- **A build fails.** The first build downloads a Go toolchain and modules, so
  the machine needs Internet access.
- **`neighbor table overflow` in `dmesg`, or hosts that miss RAs.** The
  kernel's neighbour table limit is shared by all namespaces. `setup` raises
  it (`net.ipv6.neigh.default.gc_thresh1/2/3`); inside a container it cannot,
  and `dev/docker.sh` does it for the Docker VM instead.
- **An experiment was interrupted.** Run `./exp setup` again; it starts from a
  clean state.
- **`N host(s) did not reach the '…' state`.** The message names the first
  differences. Inspect a host with `ip -n pxh001 -6 route show`, and the RA
  daemon's log in `/var/tmp/polaris-exp/proc/gorad-a.err`.
