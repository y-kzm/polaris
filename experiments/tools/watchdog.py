#!/usr/bin/env python3
"""Upstream watchdog of a site-exit router, for the failover experiments.

It watches the upstream interface. When the upstream fails it stops the RA
daemon with SIGTERM; go-ra then sends, for every RA configuration, a final RA
with Router Lifetime 0 and zero route lifetimes (the cooperative withdrawal).

Two detectors:
    link   the interface loses its carrier (rtnetlink event, no polling)
    ping   N consecutive echo requests to the upstream gateway are lost

Every step is written to the log with a timestamp, so that the detection
time can be separated from the withdrawal and switch-over times:

    <epoch> armed
    <epoch> detect <reason>
    <epoch> withdraw-signalled | withdraw-failed <why>
"""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import time


def carrier_up(iface: str) -> bool:
    try:
        with open(f"/sys/class/net/{iface}/carrier") as f:
            return f.read().strip() == "1"
    except OSError:
        return False


def wait_link_down(iface: str) -> str:
    """Block until the interface loses its carrier or is set down."""
    monitor = subprocess.Popen(["ip", "monitor", "link", "dev", iface], stdout=subprocess.PIPE, text=True, bufsize=1)
    try:
        if not carrier_up(iface):  # already down before the monitor started
            return "link down"
        assert monitor.stdout is not None
        for line in monitor.stdout:
            # "3: wan@if2: <NO-CARRIER,BROADCAST,MULTICAST,UP> ..." or, when
            # set down, flags without UP. LOWER_UP is present only with carrier.
            if "<" in line and "LOWER_UP" not in line:
                return "link down"
        return "monitor ended"
    finally:
        monitor.kill()


def wait_ping_loss(target: str, iface: str, interval: float, fails: int) -> str:
    """Block until `fails` consecutive pings to the gateway are lost."""
    lost = 0
    while lost < fails:
        started = time.time()
        ok = subprocess.run(
            ["ping", "-6", "-n", "-c", "1", "-W", str(max(1, round(interval))), "-I", iface, target],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False,
        ).returncode == 0
        lost = 0 if ok else lost + 1
        time.sleep(max(0.0, interval - (time.time() - started)))
    return f"{fails} pings to {target} lost"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--wan", required=True, help="upstream interface")
    parser.add_argument("--daemon-pidfile", required=True, help="pid file of gorad")
    parser.add_argument("--log", required=True)
    parser.add_argument("--mode", choices=("link", "ping"), default="link")
    parser.add_argument("--target", default="", help="ping mode: upstream gateway address")
    parser.add_argument("--interval", type=float, default=0.2, help="ping mode: seconds between probes")
    parser.add_argument("--fails", type=int, default=3, help="ping mode: losses that count as a failure")
    args = parser.parse_args()

    with open(args.log, "a", buffering=1) as log:
        def note(event: str) -> None:
            log.write(f"{time.time():.6f} {event}\n")

        note("armed")
        if args.mode == "link":
            reason = wait_link_down(args.wan)
        else:
            reason = wait_ping_loss(args.target, args.wan, args.interval, args.fails)
        note(f"detect {reason}")
        try:
            with open(args.daemon_pidfile) as f:
                os.kill(int(f.read().strip()), signal.SIGTERM)
            note("withdraw-signalled")
        except (OSError, ValueError) as err:
            note(f"withdraw-failed {err}")


if __name__ == "__main__":
    main()
