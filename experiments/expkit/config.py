"""Experiment configuration: one TOML file describes the site and every experiment."""

from __future__ import annotations

import ipaddress
import tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

EXP_DIR = Path(__file__).resolve().parent.parent
REPO_DIR = EXP_DIR.parent

# Prefixes of the paper's D1 (Microsoft Teams), Table "Evaluation scenario".
PAPER_D1 = (
    "2603:1027::/48", "2603:1037::/48", "2603:1047::/48", "2603:1057::/48",
    "2603:1063::/38", "2620:1ec:40::/42", "2620:1ec:6::/48",
)


class ExpError(Exception):
    """A problem the operator can act on; printed without a traceback."""


@dataclass(frozen=True)
class RouterCfg:
    """One site-exit router."""

    key: str  # "a"
    name: str  # "Router(a)"
    isp: str  # "ISP-A"
    isp_prefix: str  # the /64 delegated by the ISP
    policy_ll: str  # source address of the policy RAs
    mgmt_addr: str  # address the controller dials (gRPC)
    lan_if: str
    wan_if: str
    ssh: str = ""  # testbed mode: SSH target
    fail_cmd: str = ""  # testbed mode: how to break the upstream link
    restore_cmd: str = ""


@dataclass(frozen=True)
class RaParams:
    """Router Advertisement timing."""

    baseline_interval_ms: int = 400_000
    policy_interval_ms: int = 40_000
    router_lifetime_s: int = 1800
    reachable_time_ms: int = 0
    retransmit_time_ms: int = 0


@dataclass(frozen=True)
class Config:
    """Everything `exp` needs to know."""

    path: Path
    mode: str  # "single" or "testbed"
    workdir: str
    go_ra_dir: Path
    hosts: int
    g1_hosts: int
    lan_prefix: str
    d1: tuple[str, ...]
    default_dst: str
    d1_dst: str  # an address inside D1 that answers ping; "" = the first address of the first prefix
    ra: RaParams
    upstream: str  # "emulated" or "real"
    urls: dict[str, str]  # real upstream: {"default": url, "d1": url}
    routers: tuple[RouterCfg, ...]
    controller_ssh: str = ""
    emulator_ssh: str = ""
    emulator_bridge: str = "br-px"
    emulator_uplink: str = ""
    controller_port: int = 8080
    manage_nptv6: bool = True  # install the NPTv6 rules on the routers
    experiments: dict[str, dict[str, Any]] = field(default_factory=dict)

    def router(self, key: str) -> RouterCfg:
        """Return the router with the given key ("a", "b", ...)."""
        for router in self.routers:
            if router.key == key:
                return router
        raise ExpError(f"unknown router {key!r}")

    @property
    def d1_dsts(self) -> tuple[str, ...]:
        """One probe address inside each D1 prefix."""
        return tuple(str(ipaddress.ip_network(p)[1]) for p in self.d1)

    @property
    def d1_probe(self) -> str:
        """The D1 destination that experiments send traffic to."""
        return self.d1_dst or self.d1_dsts[0]

    def exp(self, name: str) -> dict[str, Any]:
        """Return the settings of one experiment (may be empty)."""
        return self.experiments.get(name, {})


def host_ll(index: int) -> str:
    """Link-local address of emulated host `index` (1-based), fixed by its MAC."""
    return str(ipaddress.ip_address(f"fe80::ff:fe00:{index:x}"))


def host_mac(index: int) -> str:
    """MAC address of emulated host `index`."""
    return f"02:00:00:00:{index >> 8:02x}:{index & 255:02x}"


def _go_ra_dir(raw: str) -> Path:
    candidates = [Path(raw).expanduser()] if raw else [REPO_DIR / "agent" / "go-ra", REPO_DIR.parent / "go-ra"]
    for candidate in candidates:
        if (candidate / "go.mod").exists():
            return candidate.resolve()
    raise ExpError(
        "go-ra source not found. Run `git submodule update --init` in the repository root, "
        "or set go_ra_dir in the configuration."
    )


def _d1_dst(raw: str, d1: tuple[str, ...]) -> str:
    """Check that the D1 probe destination, if given, lies inside D1."""
    if not raw:
        return ""
    addr = ipaddress.ip_address(raw)
    if not any(addr in ipaddress.ip_network(p) for p in d1):
        raise ExpError(f"policy.d1_dst {raw} is not inside any prefix of policy.d1")
    return str(addr)


def _routers(raw: list[dict[str, Any]], mode: str) -> tuple[RouterCfg, ...]:
    routers = []
    for index, entry in enumerate(raw):
        key = entry.get("key") or "abcdef"[min(index, 5)]
        if len(key) != 1 or key not in "abcdef" or key in {r.key for r in routers}:
            raise ExpError(f"router key {key!r} must be a unique letter from a to f")
        defaults: dict[str, Any] = {
            "name": f"Router({key})",
            "isp": f"ISP-{key.upper()}",
            "policy_ll": f"fe80::face:{key}",
        }
        if mode == "single":
            defaults |= {"mgmt_addr": f"fd00:ffff::{key}", "lan_if": "lan", "wan_if": "wan"}
        merged = defaults | entry | {"key": key}
        missing = [f for f in ("isp_prefix", "mgmt_addr", "lan_if", "wan_if") if not merged.get(f)]
        if mode == "testbed" and not merged.get("ssh"):
            missing.append("ssh")
        if missing:
            raise ExpError(f"router {key!r}: missing {', '.join(missing)}")
        ipaddress.ip_network(merged["isp_prefix"])
        routers.append(RouterCfg(**merged))
    if len(routers) < 2:
        raise ExpError("at least two [[router]] entries are required")
    return tuple(routers)


def load(path: Path) -> Config:
    """Read and validate a configuration file."""
    try:
        raw = tomllib.loads(path.read_text())
    except FileNotFoundError as err:
        raise ExpError(f"configuration file not found: {path}") from err
    except tomllib.TOMLDecodeError as err:
        raise ExpError(f"{path}: {err}") from err

    mode = raw.get("mode", "single")
    if mode not in ("single", "testbed"):
        raise ExpError(f"mode must be 'single' or 'testbed', not {mode!r}")
    site, policy, upstream = raw.get("site", {}), raw.get("policy", {}), raw.get("upstream", {})
    machines = raw.get("machines", {})
    hosts = int(site.get("hosts", 200))
    g1_hosts = int(policy.get("g1_hosts", max(1, hosts // 4)))
    if not 2 <= hosts <= 999:
        raise ExpError("site.hosts must be between 2 and 999")
    if not 0 < g1_hosts < hosts:
        raise ExpError("policy.g1_hosts must be between 1 and site.hosts - 1")
    kind = upstream.get("kind", "emulated")
    if kind not in ("emulated", "real"):
        raise ExpError("upstream.kind must be 'emulated' or 'real'")
    if mode == "single" and kind == "real":
        raise ExpError("a real upstream needs mode = 'testbed' (single mode builds its own ISPs)")
    if mode == "testbed" and not (machines.get("controller") and machines.get("emulator")):
        raise ExpError("testbed mode needs machines.controller and machines.emulator (SSH targets)")

    try:
        return Config(
            path=path,
            mode=mode,
            workdir=raw.get("workdir", "/var/tmp/polaris-exp"),
            go_ra_dir=_go_ra_dir(raw.get("go_ra_dir", "")),
            hosts=hosts,
            g1_hosts=g1_hosts,
            lan_prefix=site.get("lan_prefix", "fd00:1::/64"),
            d1=tuple(policy.get("d1", PAPER_D1)),
            default_dst=policy.get("default_dst", "2001:db8::1"),
            d1_dst=_d1_dst(policy.get("d1_dst", ""), tuple(policy.get("d1", PAPER_D1))),
            ra=RaParams(**raw.get("ra", {})),
            upstream=kind,
            urls=dict(upstream.get("urls", {})),
            routers=_routers(raw.get("router", []), mode),
            controller_ssh=machines.get("controller", ""),
            emulator_ssh=machines.get("emulator", ""),
            emulator_bridge=machines.get("emulator_bridge", "br-px"),
            emulator_uplink=machines.get("emulator_uplink", ""),
            controller_port=int(raw.get("controller_port", 8080)),
            manage_nptv6=bool(site.get("manage_nptv6", mode == "single")),
            experiments=raw.get("experiments", {}),
        )
    except (TypeError, ValueError) as err:
        raise ExpError(f"{path}: {err}") from err
