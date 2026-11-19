"""System verification (paper Section V-A): routing tables and first-hop selection of every host."""

from __future__ import annotations

from collections import Counter

from ..config import Config
from ..policy import EMPTY, HostState, expected, paper
from ..report import Run
from ..site import Site
from ..topology import host_ns

TITLE = "routing tables and first-hop router of every host, before and after the policy"


def _matrix(site: Site, states: dict[int, HostState], dsts: tuple[str, ...]) -> list[list[str]]:
    return [[site.owner(states[i].first_hop.get(dst, "")) or "?" for dst in dsts] for i in sorted(states)]


def run(cfg: Config, site: Site, out: Run) -> None:
    """Apply the paper scenario and compare every host with what the policy designates."""
    site.configure_ra()
    scenario = paper(cfg)
    dsts = (*cfg.d1_dsts, cfg.default_dst)
    g1 = set(scenario.groups[0].hosts)
    shown = (1, cfg.g1_hosts + 1)  # one host of each group, as in the paper's figure

    phases = {}
    for phase, policy in (("before", EMPTY), ("after", scenario)):
        site.clean_state(policy)
        phases[phase] = site.snapshot(dsts)
        for index in shown:
            table = site.emu.sh(f"ip -n {host_ns(index)} -6 route show")
            out.path(f"raw/routes_host{index:03d}_{phase}.txt").write_text(table)
        matrix = _matrix(site, phases[phase], dsts)
        out.csv(
            f"first_hop_{phase}.csv", ["host", "group", *dsts],
            [[i, "G1" if i in g1 else "G2", *row] for i, row in zip(sorted(phases[phase]), matrix)],
        )

    out.say(f"Scenario: G1 = host 1–{cfg.g1_hosts} → D1 ({len(cfg.d1)} prefixes) via "
            f"{cfg.routers[0].name};  G2 = host {cfg.g1_hosts + 1}–{cfg.hosts} → ::/0 via {cfg.routers[1].name}")
    out.say()
    rows = []
    for phase, states in phases.items():
        for group, members in (("G1", sorted(g1)), ("G2", sorted(set(states) - g1))):
            for label, targets in (("D1 prefixes", cfg.d1_dsts), (cfg.default_dst, (cfg.default_dst,))):
                counts = Counter(
                    "/".join(sorted({site.owner(states[i].first_hop.get(d, "")) or "?" for d in targets}))
                    for i in members
                )
                rows.append([phase, group, len(members), label,
                             ", ".join(f"Router({k}): {n}" for k, n in sorted(counts.items()))])
    out.table(["phase", "group", "hosts", "destination", "first-hop router (hosts)"], rows)

    # After the policy: every designated (host, destination) pair must use the designated router,
    # and the routing table must hold exactly the designated routes.
    want = expected(scenario, cfg.hosts)
    wrong = [
        (i, dst, site.owner(state.first_hop.get(dst, "")), want[i].router_for(dst))
        for i, state in phases["after"].items() for dst in dsts
        if want[i].router_for(dst) and site.owner(state.first_hop.get(dst, "")) != want[i].router_for(dst)
    ]
    problems = site.problems(scenario, phases["after"])
    designated = sum(1 for i in want for dst in dsts if want[i].router_for(dst))
    out.say(f"Designated (host, destination) pairs: {designated};  using the designated router: {designated - len(wrong)}")
    out.say(f"Hosts whose routing table matches the policy exactly: {cfg.hosts - len(problems)}/{cfg.hosts}")
    out.say()
    for i, dst, got, exp in wrong[:5]:
        out.fail(f"host {i} → {dst}: Router({got or '?'}), designated Router({exp})")
    for i, issues in list(problems.items())[:5]:
        out.fail(f"host {i}: {issues[0]}")
