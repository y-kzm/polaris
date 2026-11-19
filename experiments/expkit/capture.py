"""Packet captures (tcpdump) and parsers for the three packet kinds the experiments use."""

from __future__ import annotations

import re
import time
from dataclasses import dataclass
from pathlib import Path

from .config import ExpError
from .node import Node

FILTER_RA = "icmp6 and ip6[40] == 134"
FILTER_ECHO = "icmp6 and ip6[40] == 128"
PING_SIZE_BASE = 56  # ICMPv6 payload bytes of host 0; host i sends PING_SIZE_BASE + i
ICMP6_HEADER = 8


class Capture:
    """One tcpdump process writing decoded packets to a text file on its node."""

    def __init__(self, node: Node, iface: str, pcap_filter: str, name: str, verbose: bool = False) -> None:
        self.node, self.name = node, f"cap-{name}"
        flags = "-nn -tt -l -e -v" if verbose else "-nn -tt -l"
        # -B: a larger buffer, so that bursts of several thousand packets per second are not dropped.
        node.spawn(self.name, f"tcpdump -i {iface} {flags} -B 16384 '{pcap_filter}'")

    def wait_listening(self, timeout: float = 10) -> None:
        """Block until tcpdump has opened the interface."""
        deadline = time.time() + timeout
        while "listening on" not in self.node.machine.output(self.name, "err"):
            if time.time() > deadline:
                raise ExpError(f"tcpdump did not start: {self.node.machine.output(self.name, 'err')[-300:]}")
            time.sleep(0.1)

    def stop(self, save_to: Path | None = None) -> str:
        """Stop the capture and return its text (also saved to `save_to`)."""
        time.sleep(0.3)  # let the last packets reach the capture
        self.node.machine.stop(self.name, "INT")
        text = self.node.machine.output(self.name)
        dropped = re.search(r"(\d+) packets dropped by kernel", self.node.machine.output(self.name, "err"))
        if dropped and int(dropped.group(1)) > 0:
            print(f"  warning: {self.name} dropped {dropped.group(1)} packets")
        if save_to is not None:
            save_to.parent.mkdir(parents=True, exist_ok=True)
            save_to.write_text(text)
        return text


def start_all(captures: list[Capture]) -> None:
    """Wait until every capture is listening."""
    for capture in captures:
        capture.wait_listening()


# ── Router Advertisements ───────────────────────────────────────────────────
@dataclass(frozen=True)
class RA:
    """One captured Router Advertisement."""

    t: float
    src: str
    dst: str
    frame_len: int
    pref: str
    lifetime: int
    routes: tuple[tuple[str, int], ...]  # (prefix, lifetime)

    @property
    def is_withdrawal(self) -> bool:
        """Report whether the RA removes state: no router lifetime and no live route."""
        return self.lifetime == 0 and all(life == 0 for _, life in self.routes)


_RECORD = re.compile(r"^\d+\.\d{6} ", re.M)
_RA_ADDR = re.compile(r"([0-9a-f:]+) > ([0-9a-f:]+): \[[^\]]*\] ICMP6, router advertisement")
_RA_FRAME = re.compile(r"ethertype IPv6 \(0x86dd\), length (\d+):")
_RA_PREF = re.compile(r"pref (\w+), router lifetime (\d+)s")
_RA_ROUTE = re.compile(r"route info option[^\n]*?([0-9a-f:]+/\d+), pref=\w+, lifetime=(\d+|infinity)s?")


def parse_ras(text: str) -> list[RA]:
    """Parse `tcpdump -e -v` output of Router Advertisements."""
    starts = [m.start() for m in _RECORD.finditer(text)] + [len(text)]
    ras = []
    for begin, end in zip(starts, starts[1:]):
        record = text[begin:end]
        addr, pref = _RA_ADDR.search(record), _RA_PREF.search(record)
        if not addr or not pref:
            continue
        frame = _RA_FRAME.search(record)
        routes = tuple(
            (prefix, 0xFFFFFFFF if life == "infinity" else int(life)) for prefix, life in _RA_ROUTE.findall(record)
        )
        ras.append(RA(
            t=float(record.split(" ", 1)[0]), src=addr.group(1), dst=addr.group(2),
            frame_len=int(frame.group(1)) if frame else 0,
            pref=pref.group(1), lifetime=int(pref.group(2)), routes=routes,
        ))
    return ras


# ── echo requests ───────────────────────────────────────────────────────────
@dataclass(frozen=True)
class Echo:
    """One captured echo request, attributed to a host by its payload size."""

    t: float
    host: int
    dst: str
    seq: int


_ECHO = re.compile(
    r"^(\d+\.\d{6}) IP6 \S+ > ([0-9a-f:]+): ICMP6, echo request, id \d+, seq (\d+), length (\d+)", re.M
)


def parse_echoes(text: str) -> list[Echo]:
    """Parse `tcpdump` output of ICMPv6 echo requests sent by hostprobe."""
    return [
        Echo(float(t), int(length) - ICMP6_HEADER - PING_SIZE_BASE, dst, int(seq))
        for t, dst, seq, length in _ECHO.findall(text)
    ]


# ── TCP, attributed by source port ──────────────────────────────────────────
_TCP = re.compile(r"^\d+\.\d{6} IP6 [0-9a-f:]+\.(\d+) > [0-9a-f:]+\.(\d+): ", re.M)


def count_tcp_by_port(text: str, port_base: int, hosts: int) -> dict[int, tuple[int, int]]:
    """Count TCP packets per host as (outbound, inbound), using each host's own source port."""
    counts = {i: [0, 0] for i in range(1, hosts + 1)}
    for sport, dport in _TCP.findall(text):
        out_host, in_host = int(sport) - port_base, int(dport) - port_base
        if in_host in counts:
            counts[in_host][1] += 1
        elif out_host in counts:
            counts[out_host][0] += 1
    return {i: (c[0], c[1]) for i, c in counts.items()}


# ── ping logs ───────────────────────────────────────────────────────────────
_PING = re.compile(r"^\[(\d+\.\d+)\] \d+ bytes from .*icmp_seq=(\d+)", re.M)


def parse_ping_log(text: str) -> list[tuple[float, int]]:
    """Return (time, sequence number) of every echo reply in a `ping -D` log."""
    return [(float(t), int(seq)) for t, seq in _PING.findall(text)]
