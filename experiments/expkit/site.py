"""The running site: routers, controller, hosts — the same interface in both modes."""

from __future__ import annotations

import json
import shlex
import time
from dataclasses import asdict, dataclass, replace
from typing import Any

from . import topology as topo
from .config import EXP_DIR, Config, ExpError, RaParams, RouterCfg, host_ll
from .node import Machine, Node
from .policy import EMPTY, HostState, Scenario, expected, mismatches, parse_snapshot

GRPC_PORT = 50051


@dataclass
class Router:
    """A site-exit router and, in single mode, the ISP behind it."""

    cfg: RouterCfg
    node: Node
    isp: Node | None
    base_ll: str = ""  # source address of the baseline RA

    @property
    def proc(self) -> str:
        """Name of the router's gorad process."""
        return f"gorad-{self.cfg.key}"


class Site:
    """Handle to the experiment network."""

    def __init__(self, cfg: Config) -> None:
        self.cfg = cfg
        self.ra = cfg.ra
        self.shared_address = False
        # RA parameters currently running; they outlive one `exp` invocation.
        self._state_file = EXP_DIR / f".state-{cfg.path.stem}.json"
        self._port_block = 0
        try:
            saved = json.loads(self._state_file.read_text())
            self.ra, self.shared_address = RaParams(**saved["ra"]), saved["shared_address"]
            self._port_block = saved.get("port_block", 0)
        except (OSError, ValueError, KeyError, TypeError):
            pass  # no state yet, or an unreadable file: assume the configured parameters
        self.local = Machine("local", "", cfg.workdir)
        self.routers: dict[str, Router] = {}
        if cfg.mode == "single":
            self.emu = self.local
            self.ctl = Node(self.local, topo.CTL)
            self.srv: Node | None = Node(self.local, topo.SRV)
            self.bridge, self.bridge_if = Node(self.local, topo.SW), topo.LAN_BRIDGE
            for r in cfg.routers:
                self.routers[r.key] = Router(r, Node(self.local, topo.router_ns(r.key)), Node(self.local, topo.isp_ns(r.key)))
        else:
            self.emu = Machine("emulator", cfg.emulator_ssh, cfg.workdir)
            self.ctl = Node(Machine("controller", cfg.controller_ssh, cfg.workdir))
            self.srv = None
            self.bridge, self.bridge_if = Node(self.emu), cfg.emulator_bridge
            for r in cfg.routers:
                self.routers[r.key] = Router(r, Node(Machine(f"router-{r.key}", r.ssh, cfg.workdir)), None)

    def machines(self) -> list[Machine]:
        """Return every distinct machine of the site."""
        found: dict[int, Machine] = {}
        for machine in (self.emu, self.ctl.machine, *(r.node.machine for r in self.routers.values())):
            found.setdefault(id(machine), machine)
        return list(found.values())

    def connect(self) -> None:
        """Attach to a site built by `exp setup`."""
        if not self.ready():
            raise ExpError("the site is not running — run `./exp setup` first")
        for key in self.routers:
            self.discover(key)

    def _save_state(self) -> None:
        temp = self._state_file.with_suffix(".tmp")
        temp.write_text(json.dumps({
            "ra": asdict(self.ra), "shared_address": self.shared_address, "port_block": self._port_block,
        }))
        temp.replace(self._state_file)

    def next_port_base(self) -> int:
        """Return an unused block of 1000 TCP source ports (host i uses base + i).

        A closed connection keeps its port in TIME_WAIT for a minute, also
        across `exp` invocations, so every run takes the next block.
        """
        self._port_block = (self._port_block + 1) % 20
        self._save_state()
        return 10000 + 1000 * self._port_block

    def forget(self) -> None:
        """Drop the saved state (after teardown)."""
        self._state_file.unlink(missing_ok=True)

    def owner(self, gateway: str) -> str:
        """Return the key of the router that owns a next-hop address ("" if none)."""
        for key, router in self.routers.items():
            if gateway and gateway in (router.base_ll, router.cfg.policy_ll):
                return key
        return ""

    def host_range(self, hosts: int | None = None) -> str:
        """Return the hostprobe host specification for the first `hosts` hosts."""
        return f"1-{hosts or self.cfg.hosts}"

    # ── controller API ──────────────────────────────────────────────────────
    def api(self, method: str, path: str, body: Any = None) -> Any:
        """Call the controller API (curl runs next to the controller)."""
        url = f"http://[::1]:{self.cfg.controller_port}/api{path}"
        cmd = f"curl -sS -g -m 120 -w '\\nPXHTTP %{{http_code}}' -X {method} -H 'Content-Type: application/json'"
        if body is None:
            script = f"{cmd} {shlex.quote(url)}"
        else:
            script = f"{cmd} --data-binary @- {shlex.quote(url)} <<'PX_JSON_EOF'\n{json.dumps(body)}\nPX_JSON_EOF"
        out = self.ctl.sh(script, check=False, timeout=150)
        text, _, code = out.rpartition("\nPXHTTP ")
        if not code.strip().isdigit():
            raise ExpError("cannot reach the controller — has `./exp setup` been run?")
        if int(code) >= 400:
            raise ExpError(f"{method} {path}: HTTP {code.strip()} {text.strip()[:300]}")
        return json.loads(text) if text.strip() else None

    def ready(self) -> bool:
        """Report whether the controller answers and reaches every router."""
        try:
            statuses = self.api("GET", "/routers/status")
        except ExpError:
            return False
        return len(statuses) == len(self.routers) and all(s.get("reachable") for s in statuses.values())

    # ── routers ─────────────────────────────────────────────────────────────
    def gorad_yaml(self, router: Router) -> str:
        """Return the go-ra configuration holding the baseline RA of a router."""
        r, ra = router.cfg, self.ra
        return (
            "interfaces:\n"
            "- id: 0\n"
            "  description: baseline\n"
            f"  name: {r.lan_if}\n"
            f"  sourceAddress: {router.base_ll}\n"
            f"  raIntervalMilliseconds: {ra.baseline_interval_ms}\n"
            "  currentHopLimit: 64\n"
            "  preference: medium\n"
            f"  routerLifetimeSeconds: {ra.router_lifetime_s}\n"
            f"  reachableTimeMilliseconds: {ra.reachable_time_ms}\n"
            f"  retransmitTimeMilliseconds: {ra.retransmit_time_ms}\n"
            "  prefixes:\n"
            f"  - prefix: \"{self.cfg.lan_prefix}\"\n"
            "    onLink: true\n"
            "    autonomous: true\n"
            "    validLifetimeSeconds: 2592000\n"
            "    preferredLifetimeSeconds: 604800\n"
        )

    def discover(self, key: str) -> None:
        """Read the baseline link-local address of a router's LAN interface."""
        router = self.routers[key]
        for _ in range(30):
            out = router.node.sh(f"ip -j -6 addr show dev {router.cfg.lan_if} scope link")
            lls = [
                a["local"] for iface in json.loads(out or "[]") for a in iface.get("addr_info", [])
                if "local" in a and "tentative" not in a and a["local"] != router.cfg.policy_ll
            ]
            if lls:
                router.base_ll = lls[0]
                return
            time.sleep(0.2)
        raise ExpError(f"{router.cfg.name}: {router.cfg.lan_if} has no link-local address")

    def start_router(self, key: str) -> None:
        """Start gorad on a router with the baseline RA."""
        router = self.routers[key]
        self.discover(key)
        work = self.cfg.workdir
        router.node.machine.write(f"{work}/gorad-{key}.yaml", self.gorad_yaml(router))
        router.node.spawn(router.proc, f"{work}/bin/gorad -f {work}/gorad-{key}.yaml -a '[::]:{GRPC_PORT}'")

    def stop_router(self, key: str, signal: str = "KILL") -> None:
        """Stop gorad. TERM sends the final RAs; KILL is a crash."""
        self.routers[key].node.machine.stop(self.routers[key].proc, signal)

    def repair(self, key: str) -> None:
        """Bring a router (and its ISP) back to the configured state after a fault."""
        router = self.routers[key]
        if router.isp is not None:
            router.isp.sh(topo.isp_script(router.cfg))
        router.node.sh(topo.router_script(self.cfg, router.cfg, self.shared_address))

    def register(self) -> None:
        """Create or update the router records of the controller."""
        existing = {r["name"]: r for r in self.api("GET", "/routers")}
        for router in self.routers.values():
            r = router.cfg
            body = {
                "name": r.name, "address": r.mgmt_addr, "interface": r.lan_if,
                "policy_source_address": "" if self.shared_address else r.policy_ll,
                "ra_interval_milliseconds": self.ra.policy_interval_ms, "current_hop_limit": 64,
                "router_lifetime_seconds": self.ra.router_lifetime_s,
                "reachable_time_milliseconds": self.ra.reachable_time_ms,
                "retransmit_time_milliseconds": self.ra.retransmit_time_ms,
            }
            if r.name in existing:
                self.api("PUT", f"/routers/{existing[r.name]['id']}", existing[r.name] | body)
            else:
                self.api("POST", "/routers", body)

    def start_routers(self) -> None:
        """(Re)start every router with the current RA parameters and register them."""
        for key, router in self.routers.items():
            self.stop_router(key)
            self.repair(key)
            self.start_router(key)
        self.register()
        self._save_state()
        deadline = time.time() + 30
        while not self.ready():
            if time.time() > deadline:
                raise ExpError("the controller cannot reach every router (gRPC port 50051)")
            time.sleep(0.5)

    def configure_ra(self, ra: RaParams | None = None, shared_address: bool = False, **changes: int) -> None:
        """Switch to other RA parameters, leaving no state of the old ones on the hosts."""
        new = replace(ra or self.cfg.ra, **changes)
        alive = all(r.node.machine.alive(r.proc) for r in self.routers.values())
        if new == self.ra and shared_address == self.shared_address and alive:
            return
        if alive:
            self.apply(EMPTY)
        self.ra, self.shared_address = new, shared_address
        self.start_routers()
        self.reset_hosts()

    # ── policy ──────────────────────────────────────────────────────────────
    def apply(self, scenario: Scenario) -> dict[str, Any]:
        """Replace the controller's policy with the scenario and apply it."""
        routers = {r["name"]: r for r in self.api("GET", "/routers")}
        for group in self.api("GET", "/groups"):
            self.api("DELETE", f"/groups/{group['id']}")
        for rule in self.api("GET", "/rules"):
            self.api("DELETE", f"/rules/{rule['id']}")
        for group in scenario.groups:
            rule_ids = []
            for key, prefixes in group.rules:
                name = self.cfg.router(key).name
                rule = self.api("POST", "/rules", {
                    "comment": f"{group.name} via {name}",
                    "nexthop": routers[name]["address"],
                    "entries": [{"type": "ipv6", "value": p} for p in prefixes],
                })
                rule_ids.append(rule["id"])
            created = self.api("POST", "/groups", {"name": group.name, "members": [host_ll(i) for i in group.hosts]})
            self.api("PUT", f"/groups/{created['id']}/rules", {"rules": rule_ids})
        started = time.time()
        result = self.api("POST", "/policy/apply")
        result["seconds"] = time.time() - started
        failed = [f"{r['router']}: {r.get('error', '')}" for r in result["results"] if not r["success"]]
        if failed:
            raise ExpError("policy apply failed — " + "; ".join(failed))
        return result

    # ── hosts ───────────────────────────────────────────────────────────────
    def hostprobe(self, args: str, timeout: float = 300) -> str:
        """Run tools/hostprobe.py on the machine that holds the emulated hosts."""
        return self.emu.sh(f"python3 {self.cfg.workdir}/tools/hostprobe.py {args}", timeout=timeout)

    def snapshot(self, dsts: tuple[str, ...] = (), hosts: int | None = None) -> dict[int, HostState]:
        """Read the RA-learned routes (and first hops toward dsts) of every host."""
        raw = self.hostprobe(f"snapshot --hosts {self.host_range(hosts)} --dst {shlex.quote(','.join(dsts))}")
        return parse_snapshot(json.loads(raw))

    def problems(self, scenario: Scenario, states: dict[int, HostState] | None = None) -> dict[int, list[str]]:
        """Return, per host, how its routes differ from the scenario (empty dict if none do)."""
        states = states or self.snapshot()
        want = expected(scenario, self.cfg.hosts)
        base = {k: r.base_ll for k, r in self.routers.items()}
        pol = {k: r.cfg.policy_ll for k, r in self.routers.items()}
        found = {i: mismatches(states[i], want[i], base, pol) for i in states}
        return {i: p for i, p in found.items() if p}

    def wait_converged(self, scenario: Scenario, timeout: float = 30) -> None:
        """Wait until every host has exactly the routes the scenario designates."""
        deadline = time.time() + timeout
        while True:
            problems = self.problems(scenario)
            if not problems:
                return
            if time.time() > deadline:
                sample = "; ".join(f"host {i}: {p[0]}" for i, p in list(problems.items())[:3])
                raise ExpError(f"{len(problems)} host(s) did not reach the '{scenario.name}' state — {sample}")
            time.sleep(1)

    def reset_hosts(self) -> None:
        """Flush every host's RA state and let it solicit again (interface down/up)."""
        self.emu.sh(
            "for ns in $(ip netns list | awk '{print $1}' | grep -E '^pxh[0-9]+$'); do\n"
            "  ip -n $ns link set eth0 down; ip -n $ns link set eth0 up\n"
            "done"
        )
        self.wait_converged(EMPTY, timeout=60)
        # A new address is unusable while duplicate address detection runs
        # (about a second); until then a host would send from its link-local address.
        self.emu.sh(
            "hosts=$(ip netns list | awk '{print $1}' | grep -E '^pxh[0-9]+$')\n"
            "n=0\n"
            "while :; do\n"
            "  missing=0\n"
            "  for ns in $hosts; do\n"
            "    [ -n \"$(ip -n $ns -6 -o addr show dev eth0 scope global -tentative)\" ] || missing=$((missing + 1))\n"
            "  done\n"
            "  [ $missing -eq 0 ] && break\n"
            "  n=$((n + 1))\n"
            "  [ $n -le 100 ] || { echo \"$missing host(s) have no global address\" >&2; exit 1; }\n"
            "  sleep 0.2\n"
            "done"
        )

    def clean_state(self, scenario: Scenario) -> None:
        """Rebuild the site state from nothing: no policy, fresh hosts, then the scenario."""
        self.apply(EMPTY)
        self.reset_hosts()
        if scenario.groups:
            self.apply(scenario)
            self.wait_converged(scenario)
