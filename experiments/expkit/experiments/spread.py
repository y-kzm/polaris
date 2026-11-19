"""Unicast RA transmission spread (paper Section V-C, reviewer comment 5).

A policy RA is sent to its hosts one after another. The spread is the time
between the first and the last RA of one round. It is measured twice: where
the router sends (as in the paper) and on the hosts' link, because the
sender-side value alone says nothing about arrival times.
"""

from __future__ import annotations

import time

from ..capture import FILTER_RA, RA, Capture, parse_ras, start_all
from ..config import Config
from ..policy import DEFAULT, EMPTY, Group, Scenario
from ..report import Run, describe, ms
from ..site import Site

TITLE = "time between the first and last unicast RA of a round, at the router and on the hosts' link"
DEFAULT_CONDITIONS = ((50, 7), (150, 0), (50, 0), (100, 0), (200, 0), (50, 1), (50, 16), (50, 32))


def _rounds(ras: list[RA], interval_s: float, size: int) -> list[list[RA]]:
    """Split a capture into rounds (separated by a silence) and keep the complete ones."""
    rounds: list[list[RA]] = []
    for ra in ras:
        if rounds and ra.t - rounds[-1][-1].t < interval_s / 2:
            rounds[-1].append(ra)
        else:
            rounds.append([ra])
    return [r for r in rounds if len(r) == size]


def run(cfg: Config, site: Site, out: Run) -> None:
    """Measure the spread for each (hosts, RIOs) condition."""
    settings = cfg.exp("spread")
    interval_ms = int(settings.get("policy_interval_ms", 2_000))
    rounds_wanted = int(settings.get("rounds", 20))
    conditions = [tuple(c) for c in settings.get("conditions", DEFAULT_CONDITIONS)]
    site.configure_ra(policy_interval_ms=interval_ms)
    router = site.routers[cfg.routers[0].key]

    rows, data = [], []
    for hosts, rios in conditions:
        if hosts > cfg.hosts:
            out.say(f"skipped {hosts} hosts / {rios} RIOs: the site has only {cfg.hosts} hosts")
            continue
        prefixes = tuple(f"2001:db8:{0x1000 + n:x}::/48" for n in range(rios)) or (DEFAULT,)
        scenario = Scenario(f"spread-{hosts}-{rios}", (
            Group("G", tuple(range(1, hosts + 1)), ((router.cfg.key, prefixes),)),
        ))
        site.apply(EMPTY)
        sender = Capture(router.node, router.cfg.lan_if, FILTER_RA, "spread-tx", verbose=True)
        receiver = Capture(site.bridge, site.bridge_if, FILTER_RA, "spread-rx", verbose=True)
        start_all([sender, receiver])
        site.apply(scenario)
        time.sleep(rounds_wanted * interval_ms / 1000 + 1)

        measured = {}
        for side, capture in (("tx", sender), ("rx", receiver)):
            text = capture.stop(out.path(f"raw/{side}_{hosts}h_{rios}rio.txt"))
            policy = [ra for ra in parse_ras(text) if ra.src == router.cfg.policy_ll and not ra.is_withdrawal]
            complete = _rounds(policy, interval_ms / 1000, hosts)
            measured[side] = ([r[-1].t - r[0].t for r in complete], policy[0].frame_len if policy else 0)
        (tx, frame), (rx, _) = measured["tx"], measured["rx"]
        if not tx:
            out.fail(f"{hosts} hosts / {rios} RIOs: no complete round captured")
            continue
        s_tx, s_rx = describe(tx), describe(rx)
        rows.append([hosts, rios, frame, s_tx["n"], f"{ms(s_tx['mean'], 2)} ± {ms(s_tx['sd'], 2)}",
                     ms(s_tx["max"], 2), f"{ms(s_rx['mean'], 2)} ± {ms(s_rx['sd'], 2)}", ms(s_rx["max"], 2)])
        data += [[hosts, rios, frame, side, n, f"{v * 1000:.4f}"]
                 for side, values in (("router", tx), ("hosts_link", rx)) for n, v in enumerate(values, 1)]
    site.apply(EMPTY)

    out.csv("spread_rounds.csv", ["hosts", "rios", "frame_bytes", "side", "round", "spread_ms"], data)
    out.say(f"Policy RA interval {interval_ms} ms; a round is one RA to each host of the group.")
    out.say()
    out.table(
        ["hosts", "RIOs", "frame (bytes)", "rounds", "at router: mean ± sd (ms)", "max (ms)",
         "on hosts' link: mean ± sd (ms)", "max (ms)"],
        rows,
    )
    if cfg.mode == "single":
        out.say("Note: in single mode the router and the hosts share one machine, so the two columns differ only by "
                "the virtual link. Use testbed mode to see the effect of a real network.")
        out.say()
