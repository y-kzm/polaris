"""Policies used by the experiments and the host state each one must produce."""

from __future__ import annotations

import ipaddress
from dataclasses import dataclass, field
from typing import Any

from .config import Config

DEFAULT = "::/0"


@dataclass(frozen=True)
class Group:
    """A group of hosts and its rules: (router key, destination prefixes)."""

    name: str
    hosts: tuple[int, ...]  # 1-based host indices
    rules: tuple[tuple[str, tuple[str, ...]], ...]


@dataclass(frozen=True)
class Scenario:
    """A complete policy."""

    name: str
    groups: tuple[Group, ...] = ()


EMPTY = Scenario("none")


def paper(cfg: Config, hosts: int | None = None) -> Scenario:
    """The evaluation scenario of the paper: G1 reaches D1 via Router(a), G2 defaults to Router(b)."""
    total = hosts or cfg.hosts
    g1 = max(1, round(cfg.g1_hosts * total / cfg.hosts))
    a, b = cfg.routers[0].key, cfg.routers[1].key
    return Scenario("paper", (
        Group("G1", tuple(range(1, g1 + 1)), ((a, cfg.d1),)),
        Group("G2", tuple(range(g1 + 1, total + 1)), ((b, (DEFAULT,)),)),
    ))


def swapped(cfg: Config) -> Scenario:
    """The paper scenario with the two routers exchanged (for the transition experiment)."""
    base = paper(cfg)
    a, b = cfg.routers[0].key, cfg.routers[1].key
    return Scenario("swapped", (
        Group("G1", base.groups[0].hosts, ((b, cfg.d1),)),
        Group("G2", base.groups[1].hosts, ((a, (DEFAULT,)),)),
    ))


@dataclass
class Expected:
    """What the policy designates for one host."""

    rio: dict[str, str] = field(default_factory=dict)  # prefix -> router key
    high: set[str] = field(default_factory=set)  # routers that are high-preference default routers

    def router_for(self, dst: str) -> str:
        """Return the designated router for a destination address, or "" if none."""
        addr = ipaddress.ip_address(dst)
        matches = [p for p in self.rio if addr in ipaddress.ip_network(p)]
        if matches:
            return self.rio[max(matches, key=lambda p: ipaddress.ip_network(p).prefixlen)]
        return next(iter(self.high)) if len(self.high) == 1 else ""


def expected(scenario: Scenario, hosts: int) -> dict[int, Expected]:
    """Compute the designated routes of every host directly from the policy."""
    result = {i: Expected() for i in range(1, hosts + 1)}
    for group in scenario.groups:
        for index in group.hosts:
            for router, prefixes in group.rules:
                for prefix in prefixes:
                    if prefix == DEFAULT:
                        result[index].high.add(router)
                    else:
                        result[index].rio[str(ipaddress.ip_network(prefix))] = router
    return result


@dataclass(frozen=True)
class HostState:
    """RA-learned routes of one host, as read from its routing table."""

    defaults: tuple[tuple[str, str], ...]  # (gateway, preference)
    rio: dict[str, frozenset[str]]  # prefix -> gateways (more than one while two routers advertise it)
    first_hop: dict[str, str]  # destination -> gateway chosen by the kernel


def parse_snapshot(raw: list[dict[str, Any]]) -> dict[int, HostState]:
    """Turn the output of `hostprobe snapshot` into HostState objects."""
    states = {}
    for entry in raw:
        defaults, rio = [], {}  # type: ignore[var-annotated]
        for route in entry["routes"]:
            gateway = route.get("gateway")
            if not gateway:
                continue  # on-link prefix route
            if route.get("dst") == "default":
                defaults.append((gateway, route.get("pref", "medium")))
            else:
                rio.setdefault(str(ipaddress.ip_network(route["dst"])), set()).add(gateway)
        frozen = {prefix: frozenset(gateways) for prefix, gateways in rio.items()}
        states[entry["host"]] = HostState(tuple(sorted(defaults)), frozen, entry["get"])
    return states


def mismatches(state: HostState, want: Expected, base_ll: dict[str, str], policy_ll: dict[str, str]) -> list[str]:
    """List the differences between a host's routes and the policy (empty if none).

    base_ll and policy_ll map router keys to the two source addresses. They
    are equal in the single-address design, in which a policy RA changes the
    preference of the router's only entry instead of adding one.
    """
    problems = []
    want_rio = {prefix: frozenset({policy_ll[router]}) for prefix, router in want.rio.items()}
    for prefix in sorted(set(want_rio) | set(state.rio)):
        got, exp = state.rio.get(prefix, frozenset()), want_rio.get(prefix, frozenset())
        if got != exp:
            problems.append(f"{prefix}: via {', '.join(sorted(got)) or 'nothing'}, "
                            f"expected {', '.join(sorted(exp)) or 'no route'}")

    high = {gw for gw, pref in state.defaults if pref == "high"}
    want_high = {policy_ll[r] for r in want.high}
    if high != want_high:
        problems.append(f"high-preference default routers {sorted(high)}, expected {sorted(want_high)}")
    medium = {gw for gw, pref in state.defaults if pref == "medium"}
    for key, ll in base_ll.items():
        if ll not in medium and ll not in want_high:
            problems.append(f"no medium-preference baseline entry for router {key}")
    stray = {gw for gw, _ in state.defaults} - set(base_ll.values()) - want_high
    if stray:
        problems.append(f"unexpected default routers {sorted(stray)}")
    return problems
