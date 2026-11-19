"""Control-plane network load (paper Section V-B).

The paper computes the RA load from frame sizes and intervals (Equations 3
and 4). This experiment measures the frame sizes, evaluates the equations
with them, and checks the equations against the RA traffic actually captured
on the hosts' link.
"""

from __future__ import annotations

import time

from ..capture import FILTER_RA, Capture, parse_ras
from ..config import Config
from ..policy import paper
from ..report import Run, fmt
from ..site import Site

TITLE = "RA frame sizes and bit rate on the hosts' link"
ALL_NODES = "ff02::1"


def run(cfg: Config, site: Site, out: Run) -> None:
    """Capture RAs for a window and compare the measured bit rate with Equation 4."""
    settings = cfg.exp("load")
    baseline_ms = int(settings.get("baseline_interval_ms", 40_000))
    policy_ms = int(settings.get("policy_interval_ms", 4_000))
    window = float(settings.get("window_s", 120))
    site.configure_ra(baseline_interval_ms=baseline_ms, policy_interval_ms=policy_ms)
    scenario = paper(cfg)
    site.clean_state(scenario)
    a, b = (site.routers[r.key] for r in cfg.routers[:2])

    capture = Capture(site.bridge, site.bridge_if, FILTER_RA, "load", verbose=True)
    capture.wait_listening()
    time.sleep(window)
    ras = [ra for ra in parse_ras(capture.stop(out.path("raw/ra_capture.txt"))) if not ra.is_withdrawal]

    base = [ra for ra in ras if ra.dst == ALL_NODES]
    c1 = [ra for ra in ras if ra.src == a.cfg.policy_ll]
    c2 = [ra for ra in ras if ra.src == b.cfg.policy_ll]
    if not (base and c1 and c2):
        out.fail(f"captured {len(base)} baseline, {len(c1)} c1 and {len(c2)} c2 RAs in {window:.0f} s — "
                 "the window must cover at least one baseline interval")
        return
    l_base, l_c1, l_c2 = base[0].frame_len, c1[0].frame_len, c2[0].frame_len
    n1, n2, routers = len(scenario.groups[0].hosts), len(scenario.groups[1].hosts), len(cfg.routers)

    def equation(base_s: float, policy_s: float) -> tuple[float, float]:
        t1 = 8 * routers * l_base / base_s
        return t1, t1 + 8 * (n1 * l_c1 + n2 * l_c2) / policy_s

    out.table(["RA", "frame size (bytes)", "paper"], [
        ["baseline (L_ra,base)", l_base, 142],
        [f"c1: {len(cfg.d1)} RIOs, medium", l_c1, 258],
        ["c2: no RIO, high", l_c2, 90],
    ])

    # Unsolicited RAs only: a solicited baseline RA is a unicast answer to one host.
    measured_base = 8 * sum(ra.frame_len for ra in base) / window
    measured_all = measured_base + 8 * sum(ra.frame_len for ra in c1 + c2) / window
    t1, t2 = equation(baseline_ms / 1000, policy_ms / 1000)
    p1, p2 = equation(cfg.ra.baseline_interval_ms / 1000, cfg.ra.policy_interval_ms / 1000)
    out.table(["", "baseline only T1 (bit/s)", "with policy RAs T2 (bit/s)"], [
        [f"measured over {window:.0f} s (intervals {baseline_ms / 1000:g} s / {policy_ms / 1000:g} s)",
         fmt(measured_base), fmt(measured_all)],
        ["Equations 3 and 4, same intervals", fmt(t1), fmt(t2)],
        [f"Equations 3 and 4, intervals {cfg.ra.baseline_interval_ms / 1000:g} s / "
         f"{cfg.ra.policy_interval_ms / 1000:g} s (paper)", fmt(p1), fmt(p2)],
    ])
    out.json("load.json", {
        "frame_bytes": {"baseline": l_base, "c1": l_c1, "c2": l_c2},
        "packets": {"baseline": len(base), "c1": len(c1), "c2": len(c2)}, "window_s": window,
        "measured_bit_s": {"t1": measured_base, "t2": measured_all},
        "equation_bit_s": {"t1": t1, "t2": t2}, "equation_paper_intervals_bit_s": {"t1": p1, "t2": p2},
    })
    # The window is not a multiple of the intervals, so allow one policy interval of slack.
    tolerance = policy_ms / 1000 / window + 0.05
    if abs(measured_all - t2) / t2 > tolerance:
        out.fail(f"measured T2 differs from Equation 4 by {abs(measured_all - t2) / t2:.0%} (allowed {tolerance:.0%})")
