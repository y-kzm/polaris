"""Transition between policies (reviewer comment 2).

The controller advertises the new state before it withdraws the old one
(make-before-break). This experiment changes a running policy while every
host pings, and checks on each host that

  * no ping is lost,
  * the new route appears before the old route disappears,
  * the final routing table is exactly the new policy.

Phase 1 exchanges the routers of both groups; phase 2 removes the policy.
"""

from __future__ import annotations

import time

from ..capture import FILTER_ECHO, Capture, parse_echoes, start_all
from ..config import Config
from ..measure import EchoIndex, Recorder, Recording
from ..policy import EMPTY, Scenario, paper, swapped
from ..report import Run, describe, ms
from ..site import Site

TITLE = "changing and removing a running policy: packet loss and order of route changes"


def _order(recording: Recording, host: int, new_gw: str, old_gw: str, since: float) -> tuple[float | None, float | None]:
    """Compare, per destination, when the route via new_gw appeared and when the one via old_gw went.

    Returns (smallest margin over the destinations, time of the last removal).
    The margin is None if some destination lost its old route without a new one.
    """
    added: dict[str, float] = {}
    removed: dict[str, float] = {}
    for e in recording.host_events(host):
        if e.t < since:
            continue
        if not e.deleted and e.gateway == new_gw:
            added[e.dst] = min(e.t, added.get(e.dst, e.t))
        elif e.deleted and e.gateway == old_gw:
            removed[e.dst] = max(e.t, removed.get(e.dst, e.t))
    last = max(removed.values()) if removed else None
    if not removed or any(dst not in added for dst in removed):
        return None, last
    return min(removed[dst] - added[dst] for dst in removed), last


def _phase(cfg: Config, site: Site, out: Run, name: str, target: Scenario, moves: dict[str, tuple[str, str]]) -> list[list]:
    """Apply `target` while recording. moves maps a group to its (old, new) router key ("" = none)."""
    dsts = (cfg.default_dst, cfg.d1_probe)
    interval = float(cfg.exp("transition").get("ping_interval_s", 0.1))
    captures = {k: Capture(r.node, r.cfg.wan_if, FILTER_ECHO, f"tr-{k}") for k, r in site.routers.items()}
    start_all(list(captures.values()))
    recorder = Recorder(site, f"transition-{name}", dsts, interval)
    time.sleep(3)
    started = site.emu.sh("date +%s.%N").strip()
    result = site.apply(target)
    time.sleep(5)
    recording = recorder.stop(out.path(f"raw/{name}"))
    echoes = EchoIndex({k: parse_echoes(c.stop(out.path(f"raw/{name}_wan_{k}.txt"))) for k, c in captures.items()})
    site.wait_converged(target)

    g1 = set(paper(cfg).groups[0].hosts)
    rows = []
    for group, members in (("G1", sorted(g1)), ("G2", sorted(set(range(1, cfg.hosts + 1)) - g1))):
        old, new = moves[group]
        dst_index = 1 if group == "G1" else 0  # G1's rule covers D1, G2's the default route
        lost = sum(recording.lost(i, k) for i in members for k in range(len(dsts)))
        sent = sum(len(recording.replies.get((i, k), [])) for i in members for k in range(len(dsts))) + lost
        margins, reaction, switched = [], [], 0
        for i in members:
            margin, removed = _order(
                recording, i, site.policy_src(new) if new else "",
                site.policy_src(old) if old else "", float(started),
            )
            if old and new and margin is not None:
                margins.append(margin)
            if removed is not None:
                reaction.append(removed - float(started))
            if new and echoes.first_via(i, new, float(started), dsts[dst_index]) is not None:
                switched += 1
        stats, react = describe(margins), describe(reaction)
        ordered = sum(1 for m in margins if m > 0)
        # With a single router address, moving the default route changes the
        # preference of existing entries; no route is added or removed to order.
        checkable = bool(old and new) and not (cfg.single_address and dst_index == 0)
        rows.append([
            name, group, len(members), f"{old or '–'} → {new or 'baseline'}", f"{lost}/{sent}",
            f"{ordered}/{len(members)}" if checkable else "n/a",
            ms(stats["median"]), ms(stats["min"]), ms(react["median"]), ms(react["max"]),
        ])
        if lost:
            out.fail(f"{name}: {group} lost {lost} of {sent} pings during the transition")
        if checkable and ordered != len(members):
            out.fail(f"{name}: on {len(members) - ordered} {group} host(s) the old route was not removed after the new one appeared")
        if new and switched != len(members):
            out.fail(f"{name}: only {switched}/{len(members)} {group} hosts sent traffic through Router({new}) afterwards")
    out.say(f"{name}: the controller applied the change in {result['seconds']:.2f} s.")
    return rows


def run(cfg: Config, site: Site, out: Run) -> None:
    """Exchange the routers of both groups, then remove the policy, under continuous pings."""
    site.configure_ra()
    a, b = cfg.routers[0].key, cfg.routers[1].key
    site.clean_state(paper(cfg))
    rows = _phase(cfg, site, out, "exchange", swapped(cfg), {"G1": (a, b), "G2": (b, a)})
    rows += _phase(cfg, site, out, "remove", EMPTY, {"G1": (b, ""), "G2": (a, "")})
    out.say()
    out.table(
        ["phase", "group", "hosts", "policy router", "pings lost / sent", "new route before old removed",
         "margin: median (ms)", "min (ms)", "old route removed after apply: median (ms)", "max (ms)"],
        rows,
    )
    out.say("Margin: time from the appearance of the new route to the removal of the old one, taken per "
            "destination; the smallest value of each host is used. "
            "A positive margin means the host always had a route the policy designates.")
    out.say()
