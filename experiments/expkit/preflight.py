"""`exp check`: look at every machine before `setup` touches it. Changes nothing."""

from __future__ import annotations

import time

from .config import Config, ExpError
from .node import Machine
from .site import GRPC_PORT, Site

OK, WARN, FAIL = "ok", "WARN", "FAIL"
Finding = tuple[str, str, str, str]  # machine, level, check, detail


def _probe(machine: Machine, script: str) -> str:
    """Run a read-only script and return its trimmed output ("" if it failed)."""
    try:
        return machine.sh(script, check=False, timeout=30).strip()
    except ExpError:
        return ""


def _common(machine: Machine, findings: list[Finding], needs_build: bool) -> bool:
    """Checks for every machine. Returns False if the machine cannot be used at all."""
    name = machine.name
    started = time.time()
    try:
        answer = machine.sh("id -u; date +%s.%N; uname -r; . /etc/os-release 2>/dev/null && echo \"$PRETTY_NAME\"", timeout=30)
    except ExpError as err:
        hint = "SSH key login and password-less sudo are required" if machine.ssh else "run as root"
        findings.append((name, FAIL, "access", f"{hint}: {str(err).splitlines()[-1][:120]}"))
        return False
    lines = answer.split("\n")
    if lines[0].strip() != "0":
        findings.append((name, FAIL, "access", "commands do not run as root"))
        return False
    findings.append((name, OK, "access", f"{lines[3].strip() or 'unknown OS'}, kernel {lines[2].strip()}"))

    # The command's round trip bounds the error of this estimate.
    round_trip = time.time() - started
    offset = float(lines[1]) - (started + round_trip / 2)
    level = OK if abs(offset) < max(0.05, round_trip) else WARN
    findings.append((name, level, "clock", f"offset {offset * 1000:+.0f} ms against this machine "
                                           f"(±{round_trip * 500:.0f} ms){'' if level == OK else ': synchronise the clocks (chrony)'}"))

    missing = _probe(machine, "for c in ip tcpdump curl python3 ping; do command -v $c >/dev/null 2>&1 || printf '%s ' $c; done")
    can_install = _probe(machine, "command -v apt-get >/dev/null && echo yes") == "yes"
    if missing:
        findings.append((name, OK if can_install else FAIL, "packages",
                         f"missing: {missing}— setup installs them with apt" if can_install
                         else f"missing: {missing}and there is no apt-get: install them by hand"))
    if needs_build:
        online = _probe(machine, "curl -sS -m 8 -o /dev/null -w '%{http_code}' https://proxy.golang.org/ 2>/dev/null") != ""
        findings.append((name, OK if online else WARN, "build",
                         "can reach the Go module proxy" if online
                         else "cannot reach proxy.golang.org: the first build downloads a Go toolchain and modules"))
    return True


def _router(cfg: Config, site: Site, key: str, findings: list[Finding]) -> None:
    router = site.routers[key]
    r, machine, name = router.cfg, router.node.machine, router.node.machine.name
    for label, iface in (("lan_if", r.lan_if), ("wan_if", r.wan_if)):
        state = _probe(machine, f"cat /sys/class/net/{iface}/operstate 2>/dev/null")
        findings.append((name, OK if state in ("up", "unknown") else FAIL, label,
                         f"{iface} is {state}" if state else f"{iface} does not exist"))
    lls = _probe(machine, f"ip -6 -o addr show dev {r.lan_if} scope link 2>/dev/null | awk '{{print $4}}'").split()
    base = [ll for ll in lls if not ll.startswith(r.policy_ll + "/")]
    findings.append((name, OK if base else FAIL, "baseline address",
                     f"{base[0]} on {r.lan_if}" if base else f"{r.lan_if} has no link-local address"))
    if any(ll.startswith(r.policy_ll + "/") for ll in lls):
        findings.append((name, OK, "policy address", f"{r.policy_ll} is already on {r.lan_if} (left by an earlier setup)"))

    listener = _probe(machine, f"ss -H -ltnp 'sport = :{GRPC_PORT}' 2>/dev/null | head -1")
    # Is the listener the gorad of an earlier setup of these scripts?
    ours = cfg.workdir in _probe(
        machine, f"pid=$(cat {cfg.workdir}/proc/gorad-{key}.pid 2>/dev/null) && tr '\\0' ' ' < /proc/$pid/cmdline 2>/dev/null")
    if listener and not ours:
        findings.append((name, FAIL, "port", f"something already listens on port {GRPC_PORT}: stop the RA daemon "
                                             f"that runs on this router; setup starts its own gorad"))
    daemons = _probe(machine, "pgrep -l -x 'radvd|gorad' 2>/dev/null | awk '{print $2}' | sort -u | tr '\\n' ' '")
    if daemons and not ours:
        findings.append((name, WARN, "RA daemons", f"running: {daemons}— a second RA daemon on {r.lan_if} sends "
                                                   "RAs the experiments do not expect; stop it for the duration"))

    wan_from_ra = _probe(machine, f"ip -6 route show default dev {r.wan_if} proto ra 2>/dev/null | head -1")
    accept_ra = _probe(machine, f"cat /proc/sys/net/ipv6/conf/{r.wan_if}/accept_ra 2>/dev/null")
    forwarding = _probe(machine, "cat /proc/sys/net/ipv6/conf/all/forwarding")
    if wan_from_ra and accept_ra != "2" and forwarding != "1":
        findings.append((name, WARN, "upstream route",
                         f"the default route on {r.wan_if} comes from an RA and accept_ra is {accept_ra}: setup enables "
                         f"forwarding, after which Linux ignores RAs there. Set net.ipv6.conf.{r.wan_if}.accept_ra=2"))
    if not cfg.manage_nptv6:
        npt = _probe(machine, "ip6tables-save 2>/dev/null | grep -c -E 'SNPT|DNPT|NETMAP' || true")
        findings.append((name, OK if npt not in ("", "0") else WARN, "NPTv6",
                         f"{npt} translation rule(s) present" if npt not in ("", "0")
                         else "no NPTv6 rule found and site.manage_nptv6 is false: hosts will leave with the site prefix"))

    # Fault injection takes the LAN and upstream interfaces down.
    via = _probe(machine, "ip -6 route get \"$(echo $SSH_CONNECTION | awk '{print $1}')\" 2>/dev/null | "
                          "sed -n 's/.* dev \\([^ ]*\\).*/\\1/p'") if machine.ssh else ""
    if via in (r.lan_if, r.wan_if):
        findings.append((name, FAIL, "management path",
                         f"this SSH session reaches the router through {via}, which the failover experiment takes down: "
                         "use a separate management interface (IPv4 sessions are not checked here)"))
    elif machine.ssh:
        findings.append((name, OK, "management path", f"SSH does not use {r.lan_if} or {r.wan_if}"
                                                      + (f" (it uses {via})" if via else " (IPv4 session: check by hand)")))


def _emulator(cfg: Config, site: Site, findings: list[Finding]) -> None:
    machine, name = site.emu, site.emu.name
    rio = _probe(machine, "[ -e /proc/sys/net/ipv6/conf/all/accept_ra_rt_info_max_plen ] && echo yes")
    findings.append((name, OK if rio else WARN, "RIO support",
                     "the kernel processes Route Information Options" if rio
                     else "no CONFIG_IPV6_ROUTE_INFO: rio-shim will be used, and the results are not kernel behaviour"))
    writable = _probe(machine, "[ -w /proc/sys/net/ipv6/neigh/default/gc_thresh3 ] && echo yes")
    if not writable and cfg.hosts > 100:
        findings.append((name, WARN, "neighbour table", "the limit cannot be raised here; RAs may be dropped with this many hosts"))
    existing = _probe(machine, "ip netns list 2>/dev/null | grep -c -E '^pxh[0-9]+' || true")
    if existing not in ("", "0"):
        findings.append((name, OK, "hosts", f"{existing} host namespace(s) of an earlier setup exist; setup recreates them"))
    if cfg.mode != "testbed":
        return
    uplink = cfg.emulator_uplink
    if not uplink:
        findings.append((name, FAIL, "uplink", "machines.emulator_uplink is not set: the hosts would not reach the routers"))
        return
    state = _probe(machine, f"cat /sys/class/net/{uplink}/operstate 2>/dev/null")
    findings.append((name, OK if state in ("up", "unknown") else FAIL, "uplink",
                     f"{uplink} is {state}" if state else f"{uplink} does not exist"))
    # setup attaches the uplink to a bridge and disables IPv6 on it.
    routes = _probe(machine, f"ip route show dev {uplink} 2>/dev/null | head -3; ip -6 route show dev {uplink} 2>/dev/null | grep -v '^fe80' | head -3")
    session = _probe(machine, "ip route get \"$(echo $SSH_CONNECTION | awk '{print $1}')\" 2>/dev/null | sed -n 's/.* dev \\([^ ]*\\).*/\\1/p'")
    if session == uplink:
        findings.append((name, FAIL, "uplink", f"this SSH session uses {uplink}: attaching it to the bridge would cut the "
                                               "connection. Use a NIC that carries only the site LAN"))
    elif routes:
        findings.append((name, WARN, "uplink", f"{uplink} carries routes of this machine ({routes.splitlines()[0][:60]}…): "
                                               "they stop working when it joins the bridge"))


def check(cfg: Config) -> bool:
    """Print what `setup` would meet on every machine. Returns False if something must be fixed first."""
    site = Site(cfg)
    findings: list[Finding] = []
    usable = {}
    router_machines = {id(r.node.machine) for r in site.routers.values()}
    for machine in site.machines():
        builds = id(machine) in router_machines or machine is site.ctl.machine
        usable[id(machine)] = _common(machine, findings, needs_build=builds)

    for key, router in site.routers.items():
        if usable[id(router.node.machine)] and cfg.mode == "testbed":
            _router(cfg, site, key, findings)
    if usable[id(site.emu)]:
        _emulator(cfg, site, findings)
    if cfg.mode == "testbed" and usable[id(site.ctl.machine)]:
        ctl = site.ctl.machine
        busy = _probe(ctl, f"ss -H -ltn 'sport = :{cfg.controller_port}' 2>/dev/null | head -1")
        ours = _probe(ctl, f"cat {cfg.workdir}/proc/controller.pid 2>/dev/null")
        if busy and not ours:
            findings.append((ctl.name, FAIL, "port", f"port {cfg.controller_port} is in use: stop the controller that runs "
                                                     "here, or set controller_port"))
        for router in site.routers.values():
            reached = _probe(ctl, f"ping -6 -c 1 -W 2 {router.cfg.mgmt_addr} >/dev/null 2>&1 && echo yes")
            findings.append((ctl.name, OK if reached else FAIL, f"reach {router.cfg.name}",
                             f"{router.cfg.mgmt_addr} answers" if reached
                             else f"{router.cfg.mgmt_addr} (mgmt_addr) does not answer ping from the controller"))
        if cfg.upstream == "real" and not cfg.d1_dst:
            findings.append(("config", WARN, "policy.d1_dst", f"not set: the transition experiment pings {cfg.d1_probe}, "
                                                             "which does not answer on the Internet"))
        if not cfg.exp("failover").get("testbed_confirmed"):
            findings.append(("config", WARN, "failover", "experiments.failover.testbed_confirmed is false: the failover "
                                                         "experiment will refuse to run"))

    width = max(len(f[0]) for f in findings)
    check_width = max(len(f[2]) for f in findings)
    for machine_name, level, what, detail in findings:
        print(f"  {level:<4}  {machine_name:<{width}}  {what:<{check_width}}  {detail}")
    failed = sum(1 for f in findings if f[1] == FAIL)
    warned = sum(1 for f in findings if f[1] == WARN)
    print(f"\n{failed} to fix, {warned} warning(s). Nothing was changed.")
    return failed == 0
