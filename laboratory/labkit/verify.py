"""Compare every host with the policy stored in the controller.

Two things are checked for each host:

* control plane: the RA-learned routes in its routing table are exactly the
  ones the policy designates, via the policy address of the right router;
* data plane: an HTTP request to each service leaves through the designated
  router's ISP, as reported by the web server from the NPTv6-translated
  source address, and the reply comes back.

The expectation is derived here from the controller's rules and groups,
independently of the controller's own compiler.
"""

from __future__ import annotations

import ipaddress
import json
import time
from dataclasses import dataclass, field
from typing import Any

from .env import Host, Lab, LabError, Topology

SERVICES = {"S1": "2001:db8:f1::1", "S2": "2001:db8:f2::1"}
SETTLE_TIMEOUT_S = 15


def _prefix(value: str) -> str:
    """Return a prefix or bare address in the form `ip route` prints it."""
    network = ipaddress.ip_network(value.strip(), strict=False)
    if network.prefixlen == 0:
        return "default"
    return str(network.network_address) if network.prefixlen == 128 else str(network)


@dataclass
class Expectation:
    """What the policy designates for one host: prefix -> router names.

    "default" stands for ::/0. More than one router for a prefix means the
    rules conflict; the controller warns about it and so does verify.
    """

    routes: dict[str, set[str]] = field(default_factory=dict)

    def designated(self, destination: str) -> str | None:
        """Return the router of the longest matching rule, or None."""
        addr = ipaddress.ip_address(destination)
        best: tuple[int, set[str]] | None = None
        for prefix, routers in self.routes.items():
            network = ipaddress.ip_network("::/0" if prefix == "default" else prefix)
            if addr in network and (best is None or network.prefixlen > best[0]):
                best = (network.prefixlen, routers)
        # With conflicting rules no single router is designated.
        return next(iter(best[1])) if best and len(best[1]) == 1 else None

    def conflicts(self) -> list[str]:
        """Return the prefixes assigned to more than one router."""
        return sorted(p for p, routers in self.routes.items() if len(routers) > 1)


@dataclass
class HostReport:
    """The observed state of one host and the problems found."""

    host: Host
    egress: dict[str, str]  # service -> ISP reported by the web server
    designated: dict[str, str | None]  # service -> router name or None
    problems: list[str]


def expectations(lab: Lab, topology: Topology) -> dict[str, Expectation]:
    """Derive the per-host expectation from the controller's rules and groups."""
    routers = lab.api("GET", "/routers")
    # A rule names its router by address or by ID.
    router_name = {r["address"]: r["name"] for r in routers} | {r["id"]: r["name"] for r in routers}
    rules = {r["id"]: r for r in lab.api("GET", "/rules")}
    expected = {h.ll: Expectation() for h in topology.hosts}

    for group in lab.api("GET", "/groups"):
        members = {str(ipaddress.ip_address(m)) for m in group["members"]} & expected.keys()
        for rule_id in group["rules"]:
            rule = rules.get(rule_id)
            router = router_name.get(rule["nexthop"]) if rule else None
            if router is None:
                continue  # the controller skips such a rule with a warning
            for entry in rule["entries"]:
                try:
                    prefix = _prefix(entry["value"])
                except ValueError:
                    continue
                for member in members:
                    expected[member].routes.setdefault(prefix, set()).add(router)
    return expected


def _ra_routes(route_json: str) -> list[dict[str, Any]]:
    """Return the RA-learned routes that have a gateway, one per next hop."""
    try:
        routes = json.loads(route_json) if route_json.strip() else []
    except json.JSONDecodeError:
        return []
    out = []
    for route in routes:
        if route.get("protocol") != "ra":
            continue
        gateways = [route["gateway"]] if "gateway" in route else [n["gateway"] for n in route.get("nexthops", [])]
        out.extend({"dst": route["dst"], "gateway": g, "pref": route.get("pref", "medium")} for g in gateways)
    return out


def _check_routes(topology: Topology, expected: Expectation, routes: list[dict[str, Any]]) -> list[str]:
    """Compare a host's RA-learned routes with its expectation."""
    problems = [f"rules conflict for {p}: {sorted(expected.routes[p])}" for p in expected.conflicts()]
    seen: dict[str, set[str]] = {}
    baseline: set[str] = set()

    for route in routes:
        owner = topology.owner(route["gateway"])
        if owner is None:
            problems.append(f"route {route['dst']} via unknown next hop {route['gateway']}")
            continue
        router, kind = owner
        if kind == "base":
            if route["dst"] != "default":
                problems.append(f"route {route['dst']} uses the baseline address of {router.name}")
            elif route["pref"] != "medium":
                problems.append(f"baseline default via {router.name} has preference {route['pref']}")
            else:
                baseline.add(router.name)
            continue
        # Every policy route is recorded, so a leftover from an earlier
        # policy shows up next to the current one.
        seen.setdefault(route["dst"], set()).add(router.name)
        if route["dst"] == "default" and route["pref"] != "high":
            problems.append(f"policy default via {router.name} has preference {route['pref']}")

    if seen != expected.routes:
        show = lambda d: {p: sorted(r) for p, r in sorted(d.items())} or "none"  # noqa: E731
        problems.append(f"policy routes on host {show(seen)} != policy {show(expected.routes)}")
    missing = {r.name for r in topology.routers} - baseline
    if missing:
        problems.append(f"no baseline default router via {', '.join(sorted(missing))}")
    return problems


def baseline_ready(lab: Lab) -> bool:
    """Report whether every host has learned all routers from the baseline RAs."""
    try:
        topology = lab.topology()
    except LabError:
        return False
    outputs = lab.exec_all([h.name for h in topology.hosts], "ip", "-j", "-6", "route", "show", "default")
    wanted = {r.base_ll for r in topology.routers}
    return all(wanted <= {r["gateway"] for r in _ra_routes(outputs[h.name])} for h in topology.hosts)


def collect(lab: Lab) -> list[HostReport]:
    """Observe every host once and compare it with the policy."""
    topology = lab.topology()
    expected = expectations(lab, topology)
    names = [h.name for h in topology.hosts]
    isp_of = {r.name: r.isp for r in topology.routers}

    tables = lab.exec_all(names, "ip", "-j", "-6", "route", "show")
    replies = {
        service: lab.exec_all(names, "wget", "-q", "-T", "3", "-O", "-", f"http://[{address}]/")
        for service, address in SERVICES.items()
    }

    reports = []
    for host in topology.hosts:
        want = expected[host.ll]
        problems = _check_routes(topology, want, _ra_routes(tables[host.name]))
        egress: dict[str, str] = {}
        designated: dict[str, str | None] = {}
        for service, address in SERVICES.items():
            fields = dict(f.split("=", 1) for f in replies[service][host.name].split() if "=" in f)
            egress[service] = fields.get("isp", "no reply")
            designated[service] = want.designated(address)
            router = designated[service]
            if egress[service] == "no reply":
                problems.append(f"{service}: no HTTP reply")
            elif router and egress[service] != isp_of[router]:
                problems.append(f"{service}: left through {egress[service]}, policy designates {router}")
        reports.append(HostReport(host, egress, designated, problems))
    return reports


def run(lab: Lab) -> bool:
    """Verify all hosts, retrying briefly while RAs propagate; print a table."""
    deadline = time.monotonic() + SETTLE_TIMEOUT_S
    while True:
        reports = collect(lab)
        if not any(r.problems for r in reports) or time.monotonic() > deadline:
            break
        time.sleep(1)

    print(f"\n  {'HOST':<8}" + "".join(f"{s + ' ' + a:<30}" for s, a in SERVICES.items()) + "ROUTING TABLE")
    for report in reports:
        cells = ""
        for service in SERVICES:
            router = report.designated[service]
            note = f"(policy: {router})" if router else "(no policy)"
            cells += f"{report.egress[service] + ' ' + note:<30}"
        status = "matches policy" if not report.problems else "MISMATCH"
        print(f"  {report.host.name:<8}{cells}{status}")
        for problem in report.problems:
            print(f"           ! {problem}")

    failed = sum(1 for r in reports if r.problems)
    print(f"\n  {'PASS' if not failed else 'FAIL'}: {len(reports) - failed}/{len(reports)} hosts match the policy "
          "(routing table and egress ISP).")
    return failed == 0
