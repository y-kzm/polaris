"""Forwarding verification (reviewer comment 4).

The routing-table check shows which router a host selects. This experiment
shows that traffic is actually forwarded through the intended ISP and comes
back: every host fetches a page with curl, and both routers' upstream
interfaces are captured. Each host uses its own TCP source port, so the
captures attribute every packet to a host even after NPTv6.
"""

from __future__ import annotations

import ipaddress
import json
import re
import shlex
from collections import Counter

from ..capture import Capture, count_tcp_by_port, start_all
from ..config import Config
from ..policy import EMPTY, expected, paper
from ..report import Run
from ..site import Site

TITLE = "curl from every host and packet counts at both upstream interfaces"
_ADDR = re.compile(r"[0-9a-fA-F:]{3,39}")


def _isp_of(cfg: Config, body: str) -> str:
    """Name the ISP whose prefix contains an address in the server's answer."""
    for token in _ADDR.findall(body):
        try:
            addr = ipaddress.ip_address(token)
        except ValueError:
            continue
        for r in cfg.routers:
            if addr in ipaddress.ip_network(r.isp_prefix):
                return r.key
    return ""


def _targets(cfg: Config) -> dict[str, tuple[str, str]]:
    """Return {target: (url, probe destination)}; the destination decides the designated router.

    The targets are one address inside every prefix of D1 ("D1-1", "D1-2", ...)
    and one destination outside D1 ("default").
    """
    if cfg.upstream == "emulated":
        targets = {f"D1-{n}": (f"http://[{dst}]/", dst) for n, dst in enumerate(cfg.d1_dsts, 1)}
        targets["default"] = (f"http://[{cfg.default_dst}]/", cfg.default_dst)
        return targets
    targets = {}
    d1_urls = cfg.urls.get("d1") or []
    if isinstance(d1_urls, str):
        d1_urls = [d1_urls]
    for n, url in enumerate(d1_urls, 1):
        # A real upstream gives URLs, not addresses; every D1 address has the same designated router.
        targets[f"D1-{n}"] = (url, cfg.d1_dsts[n - 1] if n <= len(cfg.d1_dsts) else cfg.d1_probe)
    if cfg.urls.get("default"):
        targets["default"] = (cfg.urls["default"], cfg.default_dst)
    return targets


def run(cfg: Config, site: Site, out: Run) -> None:
    """Measure, before and after the policy, where every host's HTTP traffic leaves the site."""
    site.configure_ra()
    scenario = paper(cfg)
    want = expected(scenario, cfg.hosts)
    g1 = set(scenario.groups[0].hosts)
    keys = list(site.routers)
    targets = _targets(cfg)
    if not targets:
        out.fail("a real upstream needs upstream.urls.default (and optionally upstream.urls.d1, one URL per D1 prefix)")
        return

    detail, summary = [], []
    for phase, policy in (("before", EMPTY), ("after", scenario)):
        site.clean_state(policy)
        for target, (url, dst) in targets.items():
            if policy.groups:
                # With a shared RA source address, a baseline RA suspends a default-route
                # policy until the next policy RA; start every round from the applied state.
                site.wait_converged(policy)
            port_base = site.next_port_base()
            ports = f"portrange {port_base + 1}-{port_base + cfg.hosts}"
            captures = {
                k: Capture(r.node, r.cfg.wan_if, f"ip6 and tcp and {ports}", f"fwd-{k}") for k, r in site.routers.items()
            }
            start_all(list(captures.values()))
            results = json.loads(site.hostprobe(
                f"curl --hosts {site.host_range()} --url {shlex.quote(url)} --port-base {port_base}", timeout=600
            ))
            counts = {
                k: count_tcp_by_port(c.stop(out.path(f"raw/wan_{k}_{phase}_{target}.txt")), port_base, cfg.hosts)
                for k, c in captures.items()
            }

            for res in results:
                i = res["host"]
                designated = want[i].router_for(dst) if phase == "after" else ""
                seen = _isp_of(cfg, res["body"])
                carried = [k for k in keys if sum(counts[k][i]) > 0]
                ok = res["exit"] == 0 and 200 <= res["http"] < 400
                detail.append([
                    phase, target, i, "G1" if i in g1 else "G2", designated, int(ok), res["http"], seen,
                    *[n for k in keys for n in counts[k][i]], res["error"],
                ])
                if not ok:
                    out.fail(f"{phase}/{target}: host {i} got no answer ({res['error'] or 'HTTP ' + str(res['http'])})")
                elif designated and (carried != [designated] or (seen and seen != designated)):
                    out.fail(f"{phase}/{target}: host {i} left through Router({'+'.join(carried) or '?'}), "
                             f"server saw ISP of Router({seen or '?'}), designated Router({designated})")
                elif ok and any(counts[k][i][0] == 0 or counts[k][i][1] == 0 for k in carried):
                    out.fail(f"{phase}/{target}: host {i} has one-way traffic at an upstream interface")

            for group, members in (("G1", g1), ("G2", set(want) - g1)):
                rows = [r for r in detail if r[0] == phase and r[1] == target and r[2] in members]
                policy_router = next(iter({r[4] for r in rows}), "")
                isps = Counter(r[7] or "n/a" for r in rows)
                packets = [f"{sum(r[8 + 2 * n] for r in rows)} / {sum(r[9 + 2 * n] for r in rows)}" for n in range(len(keys))]
                summary.append([
                    phase, target, group, len(rows), f"Router({policy_router})" if policy_router else "none",
                    f"{sum(r[5] for r in rows)}/{len(rows)}",
                    ", ".join(f"{site.cfg.router(k).isp if k != 'n/a' else k}: {n}" for k, n in sorted(isps.items())),
                    *packets,
                ])

    header = ["phase", "target", "host", "group", "designated", "ok", "http", "isp_seen"]
    header += [f"{name}_{k}" for k in keys for name in ("pkts_out", "pkts_in")]
    out.csv("forwarding_hosts.csv", [*header, "error"], detail)
    out.say("Packets are TCP segments of the test connections seen on each router's upstream interface (out / in).")
    out.say()
    out.table(
        ["phase", "target", "group", "hosts", "policy", "HTTP ok", "ISP seen by the server",
         *[f"{site.cfg.router(k).name} upstream out / in" for k in keys]],
        summary,
    )
    if cfg.mode == "single":
        drops = {}
        for k, r in site.routers.items():
            assert r.isp is not None
            listing = r.isp.sh("ip6tables -vnxL FORWARD")
            drops[k] = sum(int(line.split()[0]) for line in listing.splitlines() if " DROP " in f" {line} ")
        out.say("Packets dropped by the ISPs' ingress filters (source address outside the ISP prefix): "
                + ", ".join(f"{site.cfg.router(k).isp} {n}" for k, n in drops.items()))
        out.say()
