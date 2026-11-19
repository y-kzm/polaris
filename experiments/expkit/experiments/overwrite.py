"""Baseline RA versus policy RA from one router (reviewer comment 1).

A host keeps one default-router entry per RA source address. If the policy RA
(preference high) and the baseline RA (preference medium) leave from the same
address, each overwrites the other. This experiment samples the default-router
entries of G2 hosts under both designs:

    separate  the policy RA has its own link-local source address (this work)
    shared    both RAs use the router's single link-local address (submitted version)
"""

from __future__ import annotations

import json
import time

from ..config import Config
from ..policy import EMPTY, paper
from ..report import Run, fmt
from ..site import Site

TITLE = "default-router preference over time: separate versus shared RA source address"


def run(cfg: Config, site: Site, out: Run) -> None:
    """Sample the hosts' default routers and report how often the policy is in force."""
    settings = cfg.exp("overwrite")
    baseline_ms = int(settings.get("baseline_interval_ms", 20_000))
    policy_ms = int(settings.get("policy_interval_ms", 8_000))
    duration = float(settings.get("duration_s", 120))
    step = float(settings.get("sample_interval_s", 0.25))
    count = min(int(settings.get("hosts", 10)), cfg.hosts - cfg.g1_hosts)
    first = cfg.g1_hosts + 1
    scenario = paper(cfg)
    b = cfg.routers[1].key

    rows, traces = [], {}
    for design in ("separate", "shared"):
        shared = design == "shared"
        site.configure_ra(baseline_interval_ms=baseline_ms, policy_interval_ms=policy_ms, shared_address=shared)
        site.apply(EMPTY)
        site.reset_hosts()
        site.apply(scenario)
        if shared:
            time.sleep(2)  # the state check below knows only the separate-address design
        else:
            site.wait_converged(scenario)
        router = site.routers[b]
        addresses = {router.base_ll} if shared else {router.base_ll, router.cfg.policy_ll}

        remote = f"{cfg.workdir}/overwrite-{design}.jsonl"
        site.hostprobe(
            f"sample --hosts {first}-{first + count - 1} --interval {step} --duration {duration} --out {remote}",
            timeout=duration + 60,
        )
        text = site.emu.read(remote)
        out.path(f"raw/samples_{design}.jsonl").write_text(text)

        per_host: dict[int, list[bool]] = {}
        for line in text.splitlines():
            sample = json.loads(line)
            high = {gw for gw, pref in sample["defaults"] if pref == "high"}
            # In force: the only high-preference default routers are Router(b)'s.
            per_host.setdefault(sample["host"], []).append(bool(high) and high <= addresses)
        total = sum(len(v) for v in per_host.values())
        enforced = sum(sum(v) for v in per_host.values())
        drops = sum(sum(1 for x, y in zip(v, v[1:]) if x and not y) for v in per_host.values())
        longest = max((_longest_false(v) for v in per_host.values()), default=0) * step
        rows.append([design, len(per_host), total, f"{100 * enforced / max(total, 1):.1f}%", drops, fmt(longest, 1)])
        traces[design] = "".join("H" if x else "m" for x in per_host.get(first, []))
        if not shared and enforced != total:
            out.fail(f"separate source addresses: the policy was in force in only {enforced}/{total} samples")
    site.configure_ra()

    out.say(f"G2 hosts {first}–{first + count - 1} (policy: default router = {cfg.router(b).name}), "
            f"sampled every {step} s for {duration:.0f} s.")
    out.say(f"Baseline RA every {baseline_ms / 1000:g} s, policy RA every {policy_ms / 1000:g} s.")
    out.say()
    out.table(
        ["RA source address", "hosts", "samples", "policy in force", "high → medium changes", "longest time not in force (s)"],
        rows,
    )
    width = int(60 / step)
    out.say(f"Host {first}, first 60 s (H = {cfg.router(b).name} is the high-preference default router, m = it is not):")
    for design, trace in traces.items():
        out.say(f"    {design:<9} {trace[:width]}")
    out.say()
    out.json("overwrite.json", {"rows": rows, "traces": traces})


def _longest_false(values: list[bool]) -> int:
    longest = current = 0
    for value in values:
        current = 0 if value else current + 1
        longest = max(longest, current)
    return longest
