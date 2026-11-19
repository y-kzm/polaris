"""Host-side recording (pings and route changes) shared by several experiments."""

from __future__ import annotations

import re
import time
from dataclasses import dataclass, field
from pathlib import Path

from .capture import PING_SIZE_BASE, Echo, parse_ping_log
from .config import ExpError
from .site import Site


@dataclass(frozen=True)
class RouteEvent:
    """One routing-table change of a host, stamped by the host machine's clock."""

    t: float
    host: int
    deleted: bool
    dst: str  # "default" or a prefix
    gateway: str
    pref: str


@dataclass
class Recording:
    """What one recording captured."""

    replies: dict[tuple[int, int], list[tuple[float, int]]] = field(default_factory=dict)  # (host, dst index)
    sent: dict[tuple[int, int], int] = field(default_factory=dict)  # echo requests ping reports as transmitted
    events: list[RouteEvent] = field(default_factory=list)

    @property
    def end(self) -> float:
        """Return the time of the last reply any host received (the recording's clock)."""
        return max((r[-1][0] for r in self.replies.values() if r), default=0.0)

    def lost(self, host: int, dst: int) -> int:
        """Count the echo requests of one host that got no reply.

        The request sent last is not counted: it may still have been in flight
        when the recording stopped.
        """
        replies = self.replies.get((host, dst), [])
        sent = self.sent.get((host, dst))
        if sent is None:  # ping was killed before it printed its summary
            return lost_pings(replies)
        return len(set(range(1, sent)) - {seq for _, seq in replies})

    def host_events(self, host: int) -> list[RouteEvent]:
        """Return the route events of one host in time order."""
        return [e for e in self.events if e.host == host]


_EVENT = re.compile(r"^(\d+\.\d+) (\d+) (Deleted )?(\S+) via (\S+) .*?proto ra\b(?:.*?pref (\w+))?", re.M)
_PING_SENT = re.compile(r"^(\d+) packets transmitted", re.M)
_PING_FILE = re.compile(r"ping_(\d+)_(\d+)\.log$")


class Recorder:
    """Runs `hostprobe record` on the host machine: pings from every host plus route monitors."""

    def __init__(self, site: Site, name: str, dsts: tuple[str, ...], interval: float, hosts: int | None = None) -> None:
        self.site, self.interval = site, interval
        self._dir = f"{site.cfg.workdir}/rec/{name}"
        site.emu.sh(f"rm -rf {self._dir}")
        site.emu.spawn(
            "record",
            f"python3 {site.cfg.workdir}/tools/hostprobe.py record --hosts {site.host_range(hosts)} "
            f"--dst {','.join(dsts)} --interval {interval} --size-base {PING_SIZE_BASE} --outdir {self._dir}",
        )
        deadline = time.time() + 60
        while site.emu.sh(f"[ -f {self._dir}/ready ] && echo yes || true").strip() != "yes":
            if time.time() > deadline:
                raise ExpError("hostprobe record did not start: " + site.emu.output("record", "err")[-300:])
            time.sleep(0.2)

    def stop(self, save_to: Path) -> Recording:
        """Stop recording, copy the logs to `save_to` and parse them."""
        self.site.emu.stop("record", "TERM", wait_s=15)
        self.site.emu.get_tree(self._dir, save_to)
        recording = Recording()
        for log in sorted(save_to.glob("ping_*.log")):
            match = _PING_FILE.search(log.name)
            if match:
                key, text = (int(match.group(1)), int(match.group(2))), log.read_text()
                recording.replies[key] = parse_ping_log(text)
                sent = _PING_SENT.search(text)
                if sent:
                    recording.sent[key] = int(sent.group(1))
        events = save_to / "route_events.log"
        if events.exists():
            for t, host, deleted, dst, gateway, pref in _EVENT.findall(events.read_text()):
                recording.events.append(RouteEvent(float(t), int(host), bool(deleted), dst, gateway, pref or "medium"))
        return recording


def lost_pings(replies: list[tuple[float, int]]) -> int:
    """Count the echo requests between the first and the last reply that got no reply."""
    if not replies:
        return 0
    seqs = {seq for _, seq in replies}
    return max(seqs) - min(seqs) + 1 - len(seqs)


def longest_gap(replies: list[tuple[float, int]], after: float, end: float, interval: float) -> tuple[float, float] | None:
    """Return (length, end time) of the longest silence that ends after `after`.

    The silence is measured from the last reply before it. None means the host
    did not recover inside the recording: it has no reply after `after`, or
    its replies stop well before `end`, the time the recording ended.
    """
    times = [t for t, _ in replies]
    later = [i for i, t in enumerate(times) if t > after]
    if not later or later[0] == 0 or end - times[-1] > max(1.0, 5 * interval):
        return None
    best = (0.0, times[later[0]])
    for i in later:
        gap = times[i] - times[i - 1]
        if gap > best[0]:
            best = (gap, times[i])
    return best


class EchoIndex:
    """Echo requests seen on the routers' upstream interfaces, indexed by host."""

    def __init__(self, echoes: dict[str, list[Echo]]) -> None:
        self._by_host: dict[int, list[tuple[float, str, str]]] = {}
        for key, packets in echoes.items():
            for echo in packets:
                self._by_host.setdefault(echo.host, []).append((echo.t, key, echo.dst))
        for packets_of_host in self._by_host.values():
            packets_of_host.sort()

    def egress_at(self, host: int, t: float, dst: str = "", max_age: float = 0.0) -> str:
        """Return the router that carried the host's last echo request before `t` ("" if none)."""
        router = ""
        for when, key, to in self._by_host.get(host, ()):
            if when > t:
                break
            if (not dst or to == dst) and (not max_age or t - when <= max_age):
                router = key
        return router

    def first_via(self, host: int, router: str, after: float, dst: str = "") -> float | None:
        """Return when the host's first echo request after `after` left through `router`."""
        for when, key, to in self._by_host.get(host, ()):
            if when > after and key == router and (not dst or to == dst):
                return when
        return None
