#!/usr/bin/env python3
"""Measurements inside the emulated hosts. Runs on the machine that holds them.

One process serves all host namespaces, so that 200 hosts cost one command
(one SSH round trip in testbed mode) instead of 200.

    snapshot  routing table and first hop per destination of every host
    record    continuous pings and route-change events until SIGTERM
    sample    default-router preferences of a few hosts at a fixed interval
    curl      one HTTP request per host, each from its own TCP source port
"""

from __future__ import annotations

import argparse
import json
import os
import selectors
import signal
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from typing import Any

WORKERS = 32


def ns_name(index: int) -> str:
    return f"pxh{index:03d}"


def run(cmd: list[str], timeout: float = 20) -> str:
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, check=False).stdout
    except subprocess.TimeoutExpired:
        return ""


def load_json(text: str) -> list[dict[str, Any]]:
    try:
        return json.loads(text) if text.strip() else []
    except json.JSONDecodeError:
        return []


def host_list(spec: str) -> list[int]:
    """Parse "1-50,60" into host indices."""
    hosts: list[int] = []
    for part in spec.split(","):
        first, _, last = part.partition("-")
        hosts += range(int(first), int(last or first) + 1)
    return hosts


# ── snapshot ────────────────────────────────────────────────────────────────
def snapshot_host(index: int, dsts: list[str]) -> dict[str, Any]:
    ns = ns_name(index)
    routes = [r for r in load_json(run(["ip", "-n", ns, "-j", "-6", "route", "show"])) if r.get("protocol") == "ra"]
    first_hop = {}
    for dst in dsts:
        got = load_json(run(["ip", "-n", ns, "-j", "-6", "route", "get", dst]))
        first_hop[dst] = got[0].get("gateway", "") if got else ""
    return {"host": index, "routes": routes, "get": first_hop}


def cmd_snapshot(args: argparse.Namespace) -> None:
    dsts = [d for d in args.dst.split(",") if d]
    with ThreadPoolExecutor(WORKERS) as pool:
        result = list(pool.map(lambda i: snapshot_host(i, dsts), host_list(args.hosts)))
    json.dump(result, sys.stdout)


# ── record ──────────────────────────────────────────────────────────────────
def cmd_record(args: argparse.Namespace) -> None:
    """Ping from every host and log every route change, until SIGTERM.

    The ICMPv6 payload size encodes the host (size_base + index): NPTv6
    rewrites the source address, so a capture on a router's upstream
    interface could not tell the hosts apart otherwise.
    """
    os.makedirs(args.outdir, exist_ok=True)
    dsts = args.dst.split(",")
    procs: list[subprocess.Popen[Any]] = []
    for index in host_list(args.hosts):
        for k, dst in enumerate(dsts):
            log = open(os.path.join(args.outdir, f"ping_{index:03d}_{k}.log"), "w")
            procs.append(subprocess.Popen(
                ["ip", "netns", "exec", ns_name(index), "ping", "-6", "-n", "-D", "-i", str(args.interval),
                 "-W", "1", "-s", str(args.size_base + index), dst],
                stdout=log, stderr=subprocess.DEVNULL,
            ))

    selector = selectors.DefaultSelector()
    for index in host_list(args.hosts):
        mon = subprocess.Popen(
            ["ip", "-n", ns_name(index), "-6", "monitor", "route"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, bufsize=0,
        )
        os.set_blocking(mon.stdout.fileno(), False)
        selector.register(mon.stdout, selectors.EVENT_READ, (index, bytearray()))
        procs.append(mon)

    stop = False

    def on_term(_signum: int, _frame: Any) -> None:
        nonlocal stop
        stop = True

    signal.signal(signal.SIGTERM, on_term)
    signal.signal(signal.SIGINT, on_term)
    with open(os.path.join(args.outdir, "ready"), "w") as ready:
        ready.write(f"{time.time():.6f}\n")

    # Each event is stamped on arrival: one clock for all hosts.
    with open(os.path.join(args.outdir, "route_events.log"), "w") as events:
        while not stop:
            for key, _ in selector.select(timeout=0.2):
                now = time.time()
                index, buf = key.data
                try:
                    chunk = os.read(key.fileobj.fileno(), 65536)
                except BlockingIOError:
                    continue
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                buf += chunk
                while b"\n" in buf:
                    line, _, rest = bytes(buf).partition(b"\n")
                    buf[:] = rest
                    events.write(f"{now:.6f} {index} {line.decode(errors='replace').strip()}\n")
            events.flush()

    for proc in procs:
        proc.send_signal(signal.SIGINT)
    for proc in procs:
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()


# ── sample ──────────────────────────────────────────────────────────────────
def cmd_sample(args: argparse.Namespace) -> None:
    hosts = host_list(args.hosts)
    end = time.time() + args.duration
    with ThreadPoolExecutor(min(WORKERS, len(hosts))) as pool, open(args.out, "w") as out:
        while time.time() < end:
            tick = time.time()

            def one(index: int) -> dict[str, Any]:
                routes = load_json(run(["ip", "-n", ns_name(index), "-j", "-6", "route", "show", "default"]))
                return {"t": time.time(), "host": index,
                        "defaults": [[r.get("gateway", ""), r.get("pref", "medium")] for r in routes]}

            for row in pool.map(one, hosts):
                out.write(json.dumps(row) + "\n")
            time.sleep(max(0.0, args.interval - (time.time() - tick)))


# ── curl ────────────────────────────────────────────────────────────────────
def curl_host(index: int, args: argparse.Namespace) -> dict[str, Any]:
    marker = "\nPXCURL %{http_code} %{time_total} %{remote_ip}"
    cmd = ["ip", "netns", "exec", ns_name(index), "curl", "-6", "-sS", "-g", "--max-time", str(args.timeout),
           "--local-port", str(args.port_base + index), "-w", marker]
    if args.resolve:
        cmd += ["--resolve", args.resolve]
    done = subprocess.run([*cmd, args.url], capture_output=True, text=True, timeout=args.timeout + 5, check=False)
    body, _, tail = done.stdout.rpartition("\nPXCURL ")
    code, total, remote = (tail.split() + ["000", "0", ""])[:3]
    return {"host": index, "port": args.port_base + index, "exit": done.returncode, "http": int(code or 0),
            "time": float(total or 0), "remote": remote, "body": body.strip()[:2000], "error": done.stderr.strip()[:300]}


def cmd_curl(args: argparse.Namespace) -> None:
    with ThreadPoolExecutor(args.parallel) as pool:
        result = list(pool.map(lambda i: curl_host(i, args), host_list(args.hosts)))
    json.dump(result, sys.stdout)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("snapshot")
    p.add_argument("--hosts", required=True)
    p.add_argument("--dst", default="")
    p.set_defaults(func=cmd_snapshot)

    p = sub.add_parser("record")
    p.add_argument("--hosts", required=True)
    p.add_argument("--dst", required=True, help="comma-separated ping destinations")
    p.add_argument("--interval", type=float, default=0.1)
    p.add_argument("--size-base", type=int, default=56)
    p.add_argument("--outdir", required=True)
    p.set_defaults(func=cmd_record)

    p = sub.add_parser("sample")
    p.add_argument("--hosts", required=True)
    p.add_argument("--interval", type=float, default=0.25)
    p.add_argument("--duration", type=float, required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(func=cmd_sample)

    p = sub.add_parser("curl")
    p.add_argument("--hosts", required=True)
    p.add_argument("--url", required=True)
    p.add_argument("--port-base", type=int, default=20000)
    p.add_argument("--timeout", type=float, default=8)
    p.add_argument("--parallel", type=int, default=16)
    p.add_argument("--resolve", default="")
    p.set_defaults(func=cmd_curl)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
