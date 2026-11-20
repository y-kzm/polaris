#!/usr/bin/env python3
"""Turn measurements taken by hand on a testbed into kit-format results.

For the two experiments of the minimal revision when they are run manually
(see MANUAL_ja.md): the forwarding verification and the cooperative failover.
The output directories have the same files as `exp run forwarding failover`
writes, so that the manuscript's data/make_values.py reads them unchanged.

    manual_analyze.py forwarding <measurement dir> --site site.json --out <results dir>
    manual_analyze.py failover   <measurement dir> --site site.json --out <results dir>

site.json describes the testbed:

    {
      "hosts": 200, "g1_hosts": 50,
      "default_dst": "2001:db8::1",
      "ping_interval_s": 0.1,
      "victim": "b",
      "routers": {
        "a": {"name": "Router(a)", "isp": "ISP-A", "isp_prefix": "2001:db8:a::/64", "ll": "fe80::a"},
        "b": {"name": "Router(b)", "isp": "ISP-B", "isp_prefix": "2001:db8:b::/64", "ll": "fe80::b"}
      },
      "host_ll": "hosts_ll.txt",
      "machine": {"os": "Ubuntu 24.04.3 LTS", "kernel": "6.8.0-85-generic"},
      "ingress_drops": {"a": 0, "b": 0}
    }

"ll" is the link-local address from which the router sends its RAs (one
address per router: the single-address design). "host_ll" names a file with
one "<host index> <link-local address>" pair per line. "ingress_drops" is
optional; leave it out if the upstream networks' filters cannot be read.

Measurement files, forwarding (phase = before|after; target = D1-1, D1-2, ...
for one address inside each prefix of D1, and default for one outside D1):
    curl_<phase>_<target>.json     output of `hostprobe.py curl`
    wan_<router>_<phase>.txt       tcpdump -nn -tt text of the upstream interface,
                                   one capture per phase that covers all targets
    ports.json                     {"before_D1-1": 21000, ...}  the --port-base of each run

Measurement files, failover (one directory trial_01, trial_02, ... per trial):
    t_fail.txt      epoch time at which the upstream was broken (router clock)
    watchdog.log    log of tools/watchdog.py on the failing router
    ra.txt          tcpdump -nn -tt -e -v text of the RAs on the hosts' link
    wan_<router>.txt  tcpdump -nn -tt text of the echo requests on each upstream interface
    hosts/          --outdir of `hostprobe.py record`
"""

from __future__ import annotations

import argparse
import csv
import ipaddress
import json
import re
import statistics
import sys
import time
from collections import Counter
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from expkit.capture import count_tcp_by_port, parse_echoes, parse_ping_log, parse_ras  # noqa: E402
from expkit.measure import EchoIndex, longest_gap  # noqa: E402

ALL_NODES = "ff02::1"
_ADDR = re.compile(r"[0-9a-fA-F:]{3,39}")
_EVENT = re.compile(r"^(\d+\.\d+) (\d+) (Deleted )?(\S+) via (\S+) .*?proto ra\b", re.M)
_PING_FILE = re.compile(r"ping_(\d+)_(\d+)\.log$")


def fail(message: str) -> None:
    sys.exit(f"manual_analyze: {message}")


def load_site(path: Path) -> dict[str, Any]:
    site = json.loads(path.read_text())
    for field in ("hosts", "g1_hosts", "routers", "machine"):
        if field not in site:
            fail(f"{path}: missing '{field}'")
    if len(site["routers"]) < 2:
        fail(f"{path}: at least two routers are required")
    for key, router in site["routers"].items():
        for field in ("isp", "isp_prefix", "ll"):
            if field not in router:
                fail(f"{path}: router {key}: missing '{field}'")
        router["ll"] = str(ipaddress.ip_address(router["ll"]))
        router.setdefault("name", f"Router({key})")
    return site


def read(path: Path) -> str:
    if not path.exists():
        fail(f"missing file: {path}")
    return path.read_text()


def write_meta(out: Path, experiment: str, site: dict[str, Any], settings: dict[str, Any]) -> None:
    out.mkdir(parents=True, exist_ok=True)
    meta = {
        "experiment": experiment, "started": time.strftime("%Y-%m-%d %H:%M:%S %z"),
        "config_file": "manual", "mode": "testbed-manual", "single_address": True,
        "hosts": site["hosts"], "g1_hosts": site["g1_hosts"], "experiment_settings": settings,
        "machines": {"testbed": site["machine"]},
    }
    (out / "meta.json").write_text(json.dumps(meta, indent=2) + "\n")


def md_table(header: list[str], rows: list[list[Any]]) -> str:
    cells = [[str(c) for c in row] for row in [header, *rows]]
    widths = [max(len(r[i]) for r in cells) for i in range(len(header))]
    lines = ["| " + " | ".join(c.ljust(w) for c, w in zip(r, widths)) + " |" for r in cells]
    lines.insert(1, "|" + "|".join("-" * (w + 2) for w in widths) + "|")
    return "\n".join(lines)


# ── forwarding ──────────────────────────────────────────────────────────────
def isp_of(site: dict[str, Any], body: str) -> str:
    for token in _ADDR.findall(body):
        try:
            addr = ipaddress.ip_address(token)
        except ValueError:
            continue
        for key, router in site["routers"].items():
            if addr in ipaddress.ip_network(router["isp_prefix"]):
                return key
    return ""


def designated(site: dict[str, Any], phase: str, target: str, host: int) -> str:
    """Router the paper's policy designates: G1 uses the first router for D1, G2 the second for everything."""
    if phase != "after":
        return ""
    first, second = list(site["routers"])[:2]
    if host <= site["g1_hosts"]:
        return first if target.startswith("D1") else ""
    return second


def forwarding_targets(ports: dict[str, int]) -> list[str]:
    """Return the targets named in ports.json; both phases must have the same ones."""
    by_phase: dict[str, list[str]] = {"before": [], "after": []}
    for tag in ports:
        phase, _, target = tag.partition("_")
        if phase not in by_phase or not target:
            fail(f"ports.json: '{tag}' is not '<before|after>_<target>'")
        by_phase[phase].append(target)
    if not by_phase["before"] or sorted(by_phase["before"]) != sorted(by_phase["after"]):
        fail("ports.json must name the same targets for 'before' and 'after'")
    if len(set(ports.values())) != len(ports):
        fail("ports.json: every run needs its own port base")
    return by_phase["before"]


def forwarding(src: Path, site: dict[str, Any], out: Path) -> None:
    keys = list(site["routers"])
    ports = json.loads(read(src / "ports.json"))
    targets = forwarding_targets(ports)
    detail, summary, problems = [], [], []
    for phase in ("before", "after"):
        captures = {k: read(src / f"wan_{k}_{phase}.txt") for k in keys}
        for target in targets:
            tag = f"{phase}_{target}"
            results = json.loads(read(src / f"curl_{tag}.json"))
            if len(results) != site["hosts"]:
                fail(f"curl_{tag}.json has {len(results)} hosts, site.json says {site['hosts']}")
            counts = {k: count_tcp_by_port(captures[k], int(ports[tag]), site["hosts"]) for k in keys}
            for res in results:
                i = res["host"]
                want = designated(site, phase, target, i)
                seen = isp_of(site, res["body"])
                carried = [k for k in keys if sum(counts[k][i]) > 0]
                ok = res["exit"] == 0 and 200 <= res["http"] < 400
                detail.append([
                    phase, target, i, "G1" if i <= site["g1_hosts"] else "G2", want, int(ok), res["http"], seen,
                    *[n for k in keys for n in counts[k][i]], res["error"],
                ])
                if not ok:
                    problems.append(f"{tag}: host {i} got no answer ({res['error'] or 'HTTP ' + str(res['http'])})")
                elif want and (carried != [want] or (seen and seen != want)):
                    problems.append(f"{tag}: host {i} left through Router({'+'.join(carried) or '?'}), "
                                    f"server saw the ISP of Router({seen or '?'}), designated Router({want})")
                elif any(counts[k][i][0] == 0 or counts[k][i][1] == 0 for k in carried):
                    problems.append(f"{tag}: host {i} has one-way traffic at an upstream interface")
            for group in ("G1", "G2"):
                rows = [r for r in detail if (r[0], r[1], r[3]) == (phase, target, group)]
                router = next(iter({r[4] for r in rows}), "")
                isps = Counter(r[7] or "n/a" for r in rows)
                packets = [f"{sum(r[8 + 2 * n] for r in rows)} / {sum(r[9 + 2 * n] for r in rows)}"
                           for n in range(len(keys))]
                summary.append([
                    phase, target, group, len(rows), f"Router({router})" if router else "none",
                    f"{sum(r[5] for r in rows)}/{len(rows)}",
                    ", ".join(f"{site['routers'][k]['isp'] if k != 'n/a' else k}: {n}" for k, n in sorted(isps.items())),
                    *packets,
                ])
    write_meta(out, "forwarding", site, {})
    header = ["phase", "target", "host", "group", "designated", "ok", "http", "isp_seen"]
    header += [f"{name}_{k}" for k in keys for name in ("pkts_out", "pkts_in")]
    with (out / "forwarding_hosts.csv").open("w", newline="") as f:
        csv.writer(f).writerows([[*header, "error"], *detail])
    text = ["# forwarding (measured by hand)", ""]
    text += [f"**FAIL**: {p}" for p in problems[:20]]
    text += ["", md_table(
        ["phase", "target", "group", "hosts", "policy", "HTTP ok", "ISP seen by the server",
         *[f"{site['routers'][k]['name']} upstream out / in" for k in keys]], summary), ""]
    drops = site.get("ingress_drops")
    if drops:
        text += ["Packets dropped by the ISPs' ingress filters (source address outside the ISP prefix): "
                 + ", ".join(f"{site['routers'][k]['isp']} {n}" for k, n in drops.items()), ""]
    text.append(f"Result: {'FAIL' if problems else 'PASS'}")
    (out / "summary.md").write_text("\n".join(text) + "\n")
    print("\n".join(text))


# ── failover ────────────────────────────────────────────────────────────────
def host_addresses(src: Path, site: dict[str, Any]) -> dict[int, str]:
    name = site.get("host_ll")
    if not name:
        fail("site.json: 'host_ll' (file with '<host index> <link-local address>' lines) is required for failover")
    table = {}
    for line in read(src / name).splitlines():
        if line.strip():
            index, address = line.split()[:2]
            table[int(index)] = str(ipaddress.ip_address(address.split("/")[0]))
    return table


def failover(src: Path, site: dict[str, Any], out: Path) -> None:
    victim_key = site.get("victim", list(site["routers"])[1])
    victim_ll = site["routers"][victim_key]["ll"]
    interval = float(site.get("ping_interval_s", 0.1))
    lls = host_addresses(src, site)
    trials = sorted(p for p in src.glob("trial_*") if p.is_dir())
    if not trials:
        fail(f"no trial_* directory in {src}")
    rows, notes = [], []
    for number, trial in enumerate(trials, 1):
        t_fail = float(read(trial / "t_fail.txt").split()[0])
        log = read(trial / "watchdog.log")
        detect = next((float(line.split()[0]) for line in log.splitlines() if " detect " in line), None)
        if detect is None:
            notes.append(f"- withdraw: trial {number}: the watchdog did not detect the failure")
        ras = parse_ras(read(trial / "ra.txt"))
        withdrawals = [ra for ra in ras if ra.is_withdrawal and ra.src == victim_ll and ra.t > t_fail - 0.5]
        if not withdrawals:
            notes.append(f"- withdraw: trial {number}: no RA with Router Lifetime 0 from {victim_ll} was captured")
        echoes = EchoIndex({k: parse_echoes(read(trial / f"wan_{k}.txt")) for k in site["routers"]})
        replies, events = {}, {}
        for path in sorted((trial / "hosts").glob("ping_*.log")):
            match = _PING_FILE.search(path.name)
            if match and int(match.group(2)) == 0:
                replies[int(match.group(1))] = parse_ping_log(path.read_text())
        for t, host, deleted, dst, gateway in _EVENT.findall(read(trial / "hosts" / "route_events.log")):
            if deleted and dst == "default" and gateway == victim_ll and float(t) > t_fail - 0.5:
                events.setdefault(int(host), []).append(float(t))
        end = max((r[-1][0] for r in replies.values() if r), default=0.0)
        affected = 0
        for i in range(1, site["hosts"] + 1):
            if echoes.egress_at(i, t_fail, max_age=1.0) != victim_key:
                continue  # the host was not using the failed router
            affected += 1
            seen = [ra.t for ra in withdrawals if ra.dst in (ALL_NODES, lls.get(i, ""))]
            ra_done = min(seen) if seen else None
            removed = events.get(i, [])
            gap = longest_gap(replies.get(i, []), t_fail, end, interval)
            rows.append([
                "withdraw", number, i, "G1" if i <= site["g1_hosts"] else "G2",
                "" if detect is None else f"{detect - t_fail:.6f}",
                "" if detect is None or ra_done is None else f"{ra_done - detect:.6f}",
                "" if ra_done is None or not removed else f"{max(removed) - ra_done:.6f}",
                "" if gap is None else f"{max(0.0, gap[0] - interval):.6f}",
                int(gap is not None),
            ])
        if not affected:
            notes.append(f"- withdraw: trial {number}: no host was sending through Router({victim_key}) at t_fail")
    write_meta(out, "failover", site, {"scenarios": ["withdraw"], "trials": len(trials), "ping_interval_s": interval})
    header = ["scenario", "trial", "host", "group", "t_detect_s", "t_withdraw_s", "t_host_s", "outage_s", "recovered"]
    with (out / "failover_hosts.csv").open("w", newline="") as f:
        csv.writer(f).writerows([header, *rows])

    def cell(column: int) -> str:
        values = [float(r[column]) * 1000 for r in rows if r[column] != ""]
        if not values:
            return "–"
        return f"{statistics.median(values):.1f} ({min(values):.1f}–{max(values):.1f})"

    stuck = sum(1 for r in rows if not r[8])
    negative = sum(1 for r in rows for c in (5, 6) if r[c] != "" and float(r[c]) < 0)
    if negative:
        notes.append(f"- {negative} negative T_withdraw/T_host value(s): the clocks of the router and of the host "
                     "machine are not synchronised well enough")
    text = ["# failover (measured by hand)", "",
            "Values are median (min–max) in milliseconds over all affected hosts of all trials.", "",
            md_table(["scenario", "trials", "host measurements", "not recovered", "T_detect", "T_withdraw",
                      "T_host", "outage"],
                     [["withdraw", len(trials), len(rows), stuck, cell(4), cell(5), cell(6), cell(7)]]),
            "", *notes, "", f"Result: {'FAIL' if notes else 'PASS'}"]
    (out / "summary.md").write_text("\n".join(text) + "\n")
    print("\n".join(text))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("experiment", choices=("forwarding", "failover"))
    parser.add_argument("measurements", type=Path)
    parser.add_argument("--site", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True, help="results directory (a subdirectory is created)")
    args = parser.parse_args()
    site = load_site(args.site)
    out = args.out / f"{args.experiment}_{time.strftime('%Y%m%d_%H%M%S')}"
    (forwarding if args.experiment == "forwarding" else failover)(args.measurements, site, out)
    print(f"\nwritten to {out}")


if __name__ == "__main__":
    main()
