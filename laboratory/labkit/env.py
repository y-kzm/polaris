"""Access to the running lab: docker compose, the controller API, the hosts."""

from __future__ import annotations

import json
import subprocess
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import Any

PROJECT = "polaris-lab"
HOST_COUNT = 10
ROUTER_SERVICES = ("router1", "router2", "router3")


class LabError(Exception):
    """A problem the user can act on; printed without a traceback."""


@dataclass(frozen=True)
class Router:
    """A site-exit router as seen from the hosts."""

    name: str  # "Router(a)"
    isp: str  # "ISP-A", what the web server reports for this router
    base_ll: str  # source of the baseline RA
    policy_ll: str  # source of the policy RAs


@dataclass(frozen=True)
class Host:
    """An end host."""

    name: str  # "host01"
    ll: str  # link-local address, the identity used in groups


@dataclass(frozen=True)
class Topology:
    """The routers and hosts of the running lab."""

    routers: tuple[Router, ...]
    hosts: tuple[Host, ...]

    def router(self, name: str) -> Router:
        """Return the router with the given name."""
        for router in self.routers:
            if router.name == name:
                return router
        raise LabError(f"unknown router {name!r}")

    def owner(self, next_hop: str) -> tuple[Router, str] | None:
        """Map a next-hop address to its router and to "base" or "policy"."""
        for router in self.routers:
            if next_hop == router.base_ll:
                return router, "base"
            if next_hop == router.policy_ll:
                return router, "policy"
        return None


class Lab:
    """Handle to the lab's compose project and controller API."""

    def __init__(self, lab_dir: Path, port: int) -> None:
        self.lab_dir = lab_dir
        self.ui_url = f"http://localhost:{port}"
        self._api = f"{self.ui_url}/api"
        self._topology: Topology | None = None
        self._kernel_rio: bool | None = None

    # ── docker ───────────────────────────────────────────────────────────────
    def compose(self, *args: str, capture: bool = True) -> str:
        """Run `docker compose` for the lab project."""
        files = ["-f", str(self.lab_dir / "docker-compose.yaml")]
        if args[:1] == ("up",) and self.kernel_has_rio():
            files += ["-f", str(self.lab_dir / "docker-compose.kernel-rio.yaml")]
        cmd = ["docker", "compose", *files, *args]
        try:
            done = subprocess.run(cmd, check=True, text=True, capture_output=capture)
        except FileNotFoundError as err:
            raise LabError("docker is not installed or not on PATH") from err
        return done.stdout if capture else ""

    def kernel_has_rio(self) -> bool:
        """Report whether the Docker host's kernel processes Route Information Options.

        Containers share that kernel. Without CONFIG_IPV6_ROUTE_INFO (Docker
        Desktop) the sysctl is absent and the hosts fall back to rio-shim.
        """
        if self._kernel_rio is None:
            probe = subprocess.run(
                ["docker", "run", "--rm", "alpine:3.21", "test", "-e",
                 "/proc/sys/net/ipv6/conf/all/accept_ra_rt_info_max_plen"],
                capture_output=True, check=False,
            )
            self._kernel_rio = probe.returncode == 0
        return self._kernel_rio

    def exec(self, service: str, *cmd: str) -> str:
        """Run a command in a lab container and return its stdout ("" on failure)."""
        done = subprocess.run(
            ["docker", "exec", f"{PROJECT}-{service}-1", *cmd],
            text=True, capture_output=True, check=False,
        )
        return done.stdout if done.returncode == 0 else ""

    def exec_all(self, services: list[str], *cmd: str) -> dict[str, str]:
        """Run the same command in several containers in parallel."""
        with ThreadPoolExecutor(max_workers=len(services) or 1) as pool:
            outputs = pool.map(lambda s: self.exec(s, *cmd), services)
        return dict(zip(services, outputs))

    # ── controller API ───────────────────────────────────────────────────────
    def api(self, method: str, path: str, body: Any = None) -> Any:
        """Call the controller API through the web UI's /api proxy."""
        data = json.dumps(body).encode() if body is not None else None
        headers = {"Content-Type": "application/json"} if data else {}
        req = urllib.request.Request(self._api + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as err:
            detail = err.read().decode(errors="replace").strip()
            raise LabError(f"{method} {path}: HTTP {err.code} {detail}") from err
        except (urllib.error.URLError, OSError) as err:
            raise LabError(f"cannot reach the controller at {self.ui_url} — is the lab up? (./lab up)") from err
        return json.loads(raw) if raw else None

    def api_ready(self) -> bool:
        """Report whether the controller API answers."""
        try:
            self.api("GET", "/routers")
        except LabError:
            return False
        return True

    def reachable_routers(self) -> int:
        """Count the registered routers whose agent answers the controller."""
        try:
            statuses = self.api("GET", "/routers/status")
        except LabError:
            return 0
        return sum(1 for s in statuses.values() if s.get("reachable"))

    # ── topology ─────────────────────────────────────────────────────────────
    def topology(self) -> Topology:
        """Discover the routers' and hosts' link-local addresses (cached)."""
        if self._topology is None:
            self._topology = Topology(routers=self._routers(), hosts=self._hosts())
        return self._topology

    def _routers(self) -> tuple[Router, ...]:
        routers = []
        for service in ROUTER_SERVICES:
            env = dict(
                line.split("=", 1) for line in self.exec(service, "env").splitlines() if "=" in line
            )
            name, policy_ll = env.get("ROUTER_LABEL", ""), env.get("POLICY_LL", "")
            lls = [
                ll
                for iface in _json(self.exec(service, "ip", "-j", "-6", "addr", "show", "scope", "link"))
                # The policy address exists only on the internal interface.
                if policy_ll in _addresses(iface)
                for ll in _addresses(iface)
            ]
            base = [ll for ll in lls if ll != policy_ll]
            if not name or len(base) != 1:
                raise LabError(f"{service} is not running or not initialised yet")
            # "Router(a)" -> "ISP-A": the name the web server reports.
            routers.append(Router(name=name, isp=f"ISP-{name[-2].upper()}", base_ll=base[0], policy_ll=policy_ll))
        return tuple(routers)

    def _hosts(self) -> tuple[Host, ...]:
        names = [f"host{n:02d}" for n in range(1, HOST_COUNT + 1)]
        outputs = self.exec_all(names, "ip", "-j", "-6", "addr", "show", "dev", "eth0", "scope", "link")
        hosts = []
        for name in names:
            lls = [ll for iface in _json(outputs[name]) for ll in _addresses(iface)]
            if not lls:
                raise LabError(f"{name} is not running")
            hosts.append(Host(name=name, ll=lls[0]))
        return tuple(hosts)


def _addresses(iface: dict[str, Any]) -> list[str]:
    """Return the addresses of one `ip -j addr` interface entry."""
    # iproute2 emits empty objects for addresses filtered out by scope.
    return [a["local"] for a in iface.get("addr_info", []) if "local" in a]


def _json(text: str) -> list[dict[str, Any]]:
    try:
        return json.loads(text) if text.strip() else []
    except json.JSONDecodeError:
        return []
