"""`exp setup` and `exp teardown`: packages, binaries, network, services."""

from __future__ import annotations

import os
import time

from . import topology as topo
from .config import EXP_DIR, REPO_DIR, Config, ExpError
from .node import Machine
from .site import Site

# command -> Debian/Ubuntu package
BASE_PACKAGES = {"ip": "iproute2", "tcpdump": "tcpdump", "curl": "curl", "python3": "python3", "ping": "iputils-ping"}
ROUTER_PACKAGES = {"ip6tables": "iptables"}
BUILD_PACKAGES = {"go": "golang-go", "gcc": "gcc"}
GO_TOOLCHAIN = "go1.25.0+auto"
RIO_SYSCTL = "/proc/sys/net/ipv6/conf/all/accept_ra_rt_info_max_plen"


def say(message: str) -> None:
    """Print one progress line."""
    print(f"  {message}", flush=True)


def install_packages(machine: Machine, packages: dict[str, str]) -> None:
    """Install the packages whose command is missing (apt)."""
    missing = machine.sh(
        "for c in " + " ".join(packages) + "; do command -v $c >/dev/null 2>&1 || echo $c; done"
    ).split()
    if not missing:
        return
    names = " ".join(sorted({packages[c] for c in missing}))
    say(f"{machine.name}: installing {names}")
    machine.sh(
        "command -v apt-get >/dev/null || { echo 'apt-get not found: install " + names + " by hand' >&2; exit 1; }\n"
        "export DEBIAN_FRONTEND=noninteractive\n"
        f"apt-get update -qq && apt-get install -y -qq {names} >/dev/null",
        timeout=1200,
    )


def build(machine: Machine, cfg: Config, target: str, rebuild: bool) -> None:
    """Build one binary on the machine that runs it: gorad, controller or rio-shim.

    The controller needs cgo (SQLite), so it is built where it runs rather
    than cross-compiled.
    """
    sources = {
        "gorad": (cfg.go_ra_dir, "./cmd/gorad", "0"),
        "controller": (REPO_DIR / "controller" / "backend", "./cmd/server", "1"),
        "rio-shim": (REPO_DIR / "laboratory" / "rio-shim", ".", "0"),
    }
    local_dir, package, cgo = sources[target]
    binary = f"{cfg.workdir}/bin/{target}"
    if not rebuild and machine.sh(f"[ -x {binary} ] && echo yes || true").strip() == "yes":
        return
    install_packages(machine, BUILD_PACKAGES)
    say(f"{machine.name}: building {target}")
    src = f"{cfg.workdir}/src/{target}"
    machine.put_tree(local_dir, src, excludes=(".git", "node_modules", "*.db"))
    machine.sh(
        f"mkdir -p {cfg.workdir}/bin {cfg.workdir}/gocache\n"
        f"cd {src}\n"
        # go-ra needs a newer Go than Ubuntu 24.04 ships; this downloads it once.
        f"HOME={cfg.workdir} GOCACHE={cfg.workdir}/gocache GOPATH={cfg.workdir}/gopath GOTOOLCHAIN={GO_TOOLCHAIN} "
        f"GOFLAGS=-buildvcs=false CGO_ENABLED={cgo} go build -o {binary} {package}",
        timeout=1800,
    )


def setup(cfg: Config, rebuild: bool = False) -> Site:
    """Build the whole site and wait until it is ready."""
    site = Site(cfg)
    if cfg.mode == "single" and os.geteuid() != 0:
        raise ExpError("single mode creates network namespaces: run as root (sudo ./exp setup)")
    teardown(cfg, quiet=True)
    site = Site(cfg)  # teardown dropped the saved state

    for machine in site.machines():
        machine.sh(f"mkdir -p {cfg.workdir}/proc {cfg.workdir}/bin")
        install_packages(machine, BASE_PACKAGES)
        machine.put_tree(EXP_DIR / "tools", f"{cfg.workdir}/tools", excludes=("__pycache__",))
    for router in site.routers.values():
        install_packages(router.node.machine, ROUTER_PACKAGES)
        build(router.node.machine, cfg, "gorad", rebuild)
    build(site.ctl.machine, cfg, "controller", rebuild)

    kernel_rio = site.emu.sh(f"[ -e {RIO_SYSCTL} ] && echo yes || true").strip() == "yes"
    if kernel_rio:
        say("hosts: the kernel processes Route Information Options")
    else:
        say("hosts: this kernel has no CONFIG_IPV6_ROUTE_INFO — using rio-shim (results are not kernel behaviour)")
        build(site.emu, cfg, "rio-shim", rebuild)

    # The neighbour table limit (1024 entries by default) is shared by all
    # namespaces of a machine; a few hundred hosts times several routers exceed it,
    # and the kernel then drops packets ("neighbor table overflow").
    raised = site.emu.sh(
        "p=/proc/sys/net/ipv6/neigh/default\n"
        "if [ -w $p/gc_thresh3 ]; then\n"
        "  [ $(cat $p/gc_thresh3) -ge 16384 ] || { echo 4096 > $p/gc_thresh1; echo 8192 > $p/gc_thresh2; "
        "echo 16384 > $p/gc_thresh3; }\n"
        "  echo yes\n"
        "fi"
    ).strip() == "yes"
    if not raised and cfg.hosts > 100:
        say("warning: cannot raise the neighbour table limit here (not the initial network namespace). "
            "With this many hosts RAs may be dropped; see README, 'Trying it without a Linux machine'.")

    say(f"network: creating {cfg.hosts} hosts")
    if cfg.mode == "single":
        site.local.sh(topo.single_links(cfg))
        assert site.srv is not None
        site.srv.sh(topo.server_script(cfg))
        site.ctl.sh(topo.controller_script())
        site.local.sh(topo.hosts_script(cfg, topo.SW, topo.LAN_BRIDGE), timeout=900)
        site.srv.spawn("webserver", f"python3 {cfg.workdir}/tools/webserver.py 80")
    else:
        site.emu.sh(topo.emulator_bridge_script(cfg))
        site.emu.sh(topo.hosts_script(cfg, "", cfg.emulator_bridge), timeout=900)
    if not kernel_rio:
        site.emu.sh(
            f"mkdir -p {cfg.workdir}/proc\n"
            "for ns in $(ip netns list | awk '{print $1}' | grep -E '^pxh[0-9]+$'); do\n"
            f"  nohup ip netns exec $ns {cfg.workdir}/bin/rio-shim eth0 >/dev/null 2>&1 </dev/null &\n"
            "done"
        )

    say("services: controller and RA daemons")
    site.ctl.sh(f"rm -f {cfg.workdir}/controller.db")
    site.ctl.spawn(
        "controller",
        f"env PORT={cfg.controller_port} DB_PATH={cfg.workdir}/controller.db {cfg.workdir}/bin/controller",
    )
    deadline = time.time() + 30
    while True:
        try:
            site.api("GET", "/routers")
            break
        except ExpError:
            if time.time() > deadline:
                raise ExpError("the controller did not start:\n" + site.ctl.machine.output("controller", "err")[-600:])
            time.sleep(0.5)
    site.start_routers()
    say("hosts: waiting for the baseline RAs")
    site.reset_hosts()
    return site


def teardown(cfg: Config, quiet: bool = False) -> None:
    """Remove everything `setup` created."""
    site = Site(cfg)
    for machine in site.machines():
        try:
            machine.sh(
                f"for f in {cfg.workdir}/proc/*.pid; do\n"
                "  [ -f \"$f\" ] || continue\n"
                "  pid=$(cat \"$f\")\n"
                f"  if {machine.ours('$pid')}; then kill -9 $pid 2>/dev/null || true; fi\n"
                "  rm -f \"$f\"\n"
                "done"
            )
        except ExpError as err:
            if not quiet:
                say(f"{machine.name}: {err}")
    site.emu.sh(topo.kill_namespaces(), check=False)
    if cfg.mode == "testbed":
        site.emu.sh(
            f"if [ -f {cfg.workdir}/created-bridge ]; then ip link del {cfg.emulator_bridge} 2>/dev/null || true; "
            f"rm -f {cfg.workdir}/created-bridge; fi",
            check=False,
        )
        for router in site.routers.values():
            router.node.sh(topo.router_cleanup(cfg, router.cfg), check=False)
    site.forget()
