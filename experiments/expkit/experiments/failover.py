"""Failover (paper Section V-D, reviewer comment 3).

Four ways for Router(b) to fail, repeated `trials` times each:

  withdraw        the upstream link fails; the watchdog detects it and the RA
                  daemon withdraws the router (cooperative, as in the paper)
  power_loss      the router disappears at once: no withdrawal is sent
  daemon_crash    the RA daemon is killed, then the upstream link fails:
                  nobody is left to send the withdrawal
  withdraw_lost   as `withdraw`, but the withdrawal RAs are dropped

For each affected host the switch-over is split into

  T_detect    fault → watchdog notices                        (router clock)
  T_withdraw  watchdog notices → last withdrawal RA on the hosts' link
  T_host      withdrawal RA on the link → route removed       (host clock)
  outage      longest silence of the host's pings             (host clock)

Hosts are affected if their traffic left through Router(b) just before the fault.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from ..capture import FILTER_ECHO, FILTER_RA, Capture, parse_echoes, parse_ras, start_all
from ..config import Config, ExpError, host_ll
from ..measure import EchoIndex, Recorder, longest_gap
from ..policy import Expected, expected, paper
from ..report import Run, describe, fmt, ms
from ..site import Router, Site

TITLE = "switch-over time under cooperative and non-cooperative router failures"
ALL_SCENARIOS = ("withdraw", "power_loss", "daemon_crash", "withdraw_lost")
LIFETIME_BOUND = ("daemon_crash", "withdraw_lost")
ALL_NODES = "ff02::1"
PRE_FAULT_S = 3.0


@dataclass(frozen=True)
class HostResult:
    """Measurements of one affected host in one trial (seconds; None = not observed)."""

    trial: int
    host: int
    group: str
    t_detect: float | None
    t_withdraw: float | None
    t_host: float | None
    outage: float | None
    recovered: bool


def _timed(router: Router, node_script: str, on_isp: bool = False) -> tuple[float, float]:
    """Run a fault-injection script and return the node's clock just before and after it."""
    node = router.isp if on_isp and router.isp is not None else router.node
    out = node.sh(f"date +%s.%N\n{node_script}\ndate +%s.%N").split()
    return float(out[0]), float(out[-1])


def _upstream(router: Router, up: bool) -> tuple[float, float]:
    """Break or restore the router's upstream link."""
    r = router.cfg
    if router.isp is not None:
        # Single mode: the ISP end goes down, so the router loses its carrier.
        return _timed(router, f"ip link set cust {'up' if up else 'down'}", on_isp=True)
    command = (r.restore_cmd if up else r.fail_cmd) or f"ip link set {r.wan_if} {'up' if up else 'down'}"
    return _timed(router, command)


class Trial:
    """One failure and its measurement."""

    def __init__(self, cfg: Config, site: Site, out: Run, scenario: str, number: int, window: float) -> None:
        self.cfg, self.site, self.out = cfg, site, out
        self.scenario, self.number, self.window = scenario, number, window
        self.victim = site.routers[cfg.routers[1].key]
        self.interval = float(cfg.exp("failover").get("ping_interval_s", 0.1))
        self.work = cfg.workdir
        self.raw = out.path(f"raw/{scenario}_{number:02d}")

    def run(self, want: dict[int, Expected]) -> tuple[list[HostResult], dict[str, float]]:
        """Inject the fault, observe for the window, and return per-host results."""
        site, victim, key = self.site, self.victim, self.victim.cfg.key
        machine = victim.node.machine
        log = f"{self.work}/watchdog-{key}.log"
        captures = {k: Capture(r.node, r.cfg.wan_if, FILTER_ECHO, f"fo-{k}") for k, r in site.routers.items()}
        ra_capture = Capture(site.bridge, site.bridge_if, FILTER_RA, "fo-ra", verbose=True)
        start_all([*captures.values(), ra_capture])
        recorder = Recorder(site, "failover", (self.cfg.default_dst,), self.interval)
        machine.sh(f"rm -f {log}")
        if self.scenario != "power_loss":
            victim.node.spawn(
                f"watchdog-{key}",
                f"python3 {self.work}/tools/watchdog.py --wan {victim.cfg.wan_if} "
                f"--daemon-pidfile {self.work}/proc/{victim.proc}.pid --log {log}",
            )
        time.sleep(PRE_FAULT_S)

        try:
            t_fail, t_fail_end = self._inject()
            time.sleep(self.window)
        finally:
            recording = recorder.stop(self.raw / "hosts")
            echoes = EchoIndex({
                k: parse_echoes(c.stop(self.raw / f"wan_{k}.txt")) for k, c in captures.items()
            })
            ras = parse_ras(ra_capture.stop(self.raw / "ra.txt"))
            machine.stop(f"watchdog-{key}", "KILL")
        watchdog = machine.read(log)
        (self.raw / "watchdog.log").write_text(watchdog)
        detect = next((float(line.split()[0]) for line in watchdog.splitlines() if " detect " in line), None)

        withdrawals = [ra for ra in ras if ra.is_withdrawal and ra.t > t_fail - 0.5]
        g1 = set(paper(self.cfg).groups[0].hosts)
        results = []
        for i in range(1, self.cfg.hosts + 1):
            if echoes.egress_at(i, t_fail, max_age=1.0) != key:
                continue  # the host was not using the failed router
            # Withdrawals this host needs: the baseline entry, and the policy entry if it has one.
            sources = {victim.base_ll}
            if key in want[i].high or key in want[i].rio.values():
                sources.add(victim.cfg.policy_ll)
            seen = [
                min((ra.t for ra in withdrawals if ra.src == src and ra.dst in (ALL_NODES, host_ll(i))), default=None)
                for src in sources
            ]
            ra_done = max(seen) if all(t is not None for t in seen) else None
            removed = [
                e.t for e in recording.host_events(i)
                if e.deleted and e.dst == "default" and e.gateway in sources and e.t > t_fail - 0.5
            ]
            gap = longest_gap(recording.replies.get((i, 0), []), t_fail, recording.end, self.interval)
            outage = None if gap is None else max(0.0, gap[0] - self.interval)
            results.append(HostResult(
                trial=self.number, host=i, group="G1" if i in g1 else "G2",
                t_detect=None if detect is None else detect - t_fail,
                t_withdraw=None if detect is None or ra_done is None else ra_done - detect,
                t_host=None if ra_done is None or not removed else max(removed) - ra_done,
                outage=outage, recovered=gap is not None,
            ))
        if self.number == 1:
            self._timeline(echoes, t_fail)
        from_victim = [ra for ra in withdrawals if ra.src in (victim.base_ll, victim.cfg.policy_ll)]
        return results, {"t_fail": t_fail, "inject_s": t_fail_end - t_fail, "withdrawals": len(from_victim)}

    def _inject(self) -> tuple[float, float]:
        victim, proc = self.victim, f"{self.work}/proc/{self.victim.proc}.pid"
        r = victim.cfg
        if self.scenario == "withdraw":
            return _upstream(victim, up=False)
        if self.scenario == "power_loss":
            # No process gets the chance to react, and the router stops forwarding and answering.
            return _timed(victim, f"kill -9 $(cat {proc}) 2>/dev/null || true\n"
                                  f"ip link set {r.lan_if} down\nip link set {r.wan_if} down")
        if self.scenario == "daemon_crash":
            victim.node.sh(f"kill -9 $(cat {proc}) 2>/dev/null || true; rm -f {proc}")
            time.sleep(float(self.cfg.exp("failover").get("crash_to_fault_s", 5)))
            return _upstream(victim, up=False)
        # withdraw_lost: the daemon sends its final RAs, but none leaves the router.
        victim.node.sh(f"ip6tables -I OUTPUT -o {r.lan_if} -p ipv6-icmp --icmpv6-type 134 -j DROP")
        return _upstream(victim, up=False)

    def undo_fault(self) -> None:
        """Remove what the fault injection changed on the router (safe to call at any time)."""
        victim, r = self.victim, self.victim.cfg
        victim.node.sh(
            f"ip6tables -D OUTPUT -o {r.lan_if} -p ipv6-icmp --icmpv6-type 134 -j DROP 2>/dev/null || true\n"
            f"ip link set {r.lan_if} up\nip link set {r.wan_if} up"
        )
        _upstream(victim, up=True)

    def restore(self) -> None:
        """Undo the fault and bring the site back to the paper scenario with fresh hosts."""
        victim, key, r = self.victim, self.victim.cfg.key, self.victim.cfg
        self.undo_fault()
        self.site.stop_router(key)
        self.site.repair(key)
        self.site.start_router(key)
        deadline = time.time() + 30
        while not self.site.ready():
            if time.time() > deadline:
                raise ExpError(f"{r.name} did not come back after the fault")
            time.sleep(0.5)
        self.site.clean_state(paper(self.cfg))

    def _timeline(self, echoes: EchoIndex, t_fail: float) -> None:
        """Write, for the first trial, how many hosts used each router at 300 ms ticks (the paper's figure)."""
        keys = list(self.site.routers)
        rows, tick = [], -PRE_FAULT_S
        while tick <= self.window:
            used = [echoes.egress_at(i, t_fail + tick, max_age=1.0) for i in range(1, self.cfg.hosts + 1)]
            rows.append([f"{tick:.1f}", *[used.count(k) for k in keys], used.count("")])
            tick += 0.3
        self.out.csv(f"timeline_{self.scenario}.csv", ["t_s", *[f"router_{k}" for k in keys], "no_traffic"], rows)


def run(cfg: Config, site: Site, out: Run) -> None:
    """Run every configured failure scenario and report the switch-over statistics."""
    settings = cfg.exp("failover")
    scenarios = [s for s in settings.get("scenarios", ALL_SCENARIOS)]
    unknown = set(scenarios) - set(ALL_SCENARIOS)
    if unknown:
        raise ExpError(f"unknown failover scenario(s): {', '.join(sorted(unknown))}")
    if cfg.mode == "testbed" and not settings.get("testbed_confirmed"):
        raise ExpError(
            "failover takes the victim router's LAN and upstream interfaces down. On real machines, make sure "
            "SSH and the controller reach the router over another interface, then set "
            "experiments.failover.testbed_confirmed = true"
        )
    lifetime = int(settings.get("bound_lifetime_s", 60))
    bound_interval = int(settings.get("bound_interval_ms", 20_000))
    want = expected(paper(cfg), cfg.hosts)

    table, detail, notes = [], [], []
    for scenario in scenarios:
        bound = scenario in LIFETIME_BOUND
        trials = int(settings.get("bound_trials" if bound else "trials", 5 if bound else 10))
        if bound:
            # A short lifetime keeps a trial short; the outage scales with it.
            site.configure_ra(router_lifetime_s=lifetime, baseline_interval_ms=bound_interval,
                              policy_interval_ms=bound_interval)
            window = lifetime + 15.0
        else:
            site.configure_ra()
            window = float(settings.get("power_loss_window_s", 75)) if scenario == "power_loss" else 6.0
        site.clean_state(paper(cfg))

        results: list[HostResult] = []
        failed_trials = []
        for number in range(1, trials + 1):
            print(f"  {scenario}: trial {number}/{trials}", flush=True)
            trial = Trial(cfg, site, out, scenario, number, window)
            try:
                found, info = trial.run(want)
                results += found
                if not found:
                    failed_trials.append(f"trial {number}: no host was using {trial.victim.cfg.name}")
                if scenario == "withdraw_lost" and info["withdrawals"]:
                    failed_trials.append(f"trial {number}: {info['withdrawals']} withdrawal RA(s) reached the "
                                         "hosts' link although they should have been dropped")
                    out.ok = False
            except ExpError as err:
                failed_trials.append(f"trial {number}: {err}")
            except KeyboardInterrupt:
                trial.undo_fault()  # never leave the router with its links down
                raise
            try:
                trial.restore()
            except ExpError as err:
                failed_trials.append(f"trial {number}: reset failed — {err}")
                site.start_routers()
                site.clean_state(paper(cfg))

        detail += [[scenario, r.trial, r.host, r.group, *(fmt(v, 6) for v in (r.t_detect, r.t_withdraw, r.t_host, r.outage)),
                    int(r.recovered)] for r in results]
        stuck = [r for r in results if not r.recovered]
        row = [scenario, trials, len(results), len(stuck)]
        for field in ("t_detect", "t_withdraw", "t_host", "outage"):
            stats = describe([getattr(r, field) for r in results if getattr(r, field) is not None])
            row.append(f"{ms(stats['median'])} ({ms(stats['min'])}–{ms(stats['max'])})" if stats["n"] else "–")
        table.append(row)
        if stuck:
            notes.append(f"{scenario}: {len(stuck)} host measurement(s) did not recover within the {window:.0f} s window "
                         f"(trials {sorted({r.trial for r in stuck})}).")
        notes += [f"{scenario}: {note}" for note in failed_trials]
        if bound:
            notes.append(f"{scenario}: Router Lifetime {lifetime} s, RAs every {bound_interval / 1000:g} s. "
                         f"Without a withdrawal the entry stays until its lifetime runs out, so the outage is "
                         f"bounded by the Router Lifetime.")
        if scenario == "withdraw" and (stuck or failed_trials):
            out.ok = False
    site.configure_ra()

    out.csv("failover_hosts.csv",
            ["scenario", "trial", "host", "group", "t_detect_s", "t_withdraw_s", "t_host_s", "outage_s", "recovered"], detail)
    out.say("Values are median (min–max) in milliseconds over all affected hosts of all trials.")
    out.say(f"The outage is measured with pings every {float(settings.get('ping_interval_s', 0.1)) * 1000:.0f} ms, "
            "which is its resolution: a value below that interval means that no ping was lost.")
    out.say()
    out.table(["scenario", "trials", "host measurements", "not recovered", "T_detect", "T_withdraw", "T_host", "outage"], table)
    for note in notes:
        out.say(f"- {note}")
    out.say()
