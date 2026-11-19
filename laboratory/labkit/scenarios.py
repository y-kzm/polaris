"""Built-in policies of the lab and how to apply them through the API."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from .env import Lab

SERVICE_PREFIX = "2001:db8:f1::/48"  # contains the web server's S1 address
DEFAULT = "::/0"


@dataclass(frozen=True)
class GroupSpec:
    """A group of hosts and the rules assigned to it."""

    name: str
    hosts: tuple[int, ...]  # 1-based host numbers
    rules: tuple[tuple[str, str], ...]  # (router name, destination prefix)

    def describe(self) -> str:
        """Return a one-line description such as "G1 host01-05: S1 via Router(a)"."""
        hosts = f"host{self.hosts[0]:02d}-{self.hosts[-1]:02d}"
        rules = ", ".join(
            f"{'default' if prefix == DEFAULT else prefix} via {router}" for router, prefix in self.rules
        )
        return f"{self.name} {hosts}: {rules}"


@dataclass(frozen=True)
class Scenario:
    """A complete policy: every group and rule the controller should hold."""

    name: str
    summary: str
    groups: tuple[GroupSpec, ...]


def _hosts(first: int, last: int) -> tuple[int, ...]:
    return tuple(range(first, last + 1))


EMPTY = Scenario("clear", "no policy: every host chooses its router on its own", ())

SCENARIOS: dict[str, Scenario] = {
    s.name: s
    for s in (
        Scenario(
            "paper",
            "the example of the paper: per-destination (RIO) and default-router (DRP) policies",
            (
                GroupSpec("G1", _hosts(1, 5), (("Router(a)", SERVICE_PREFIX),)),
                GroupSpec("G2", _hosts(6, 10), (("Router(b)", DEFAULT),)),
            ),
        ),
        Scenario(
            "swap",
            "both groups move to other routers: new routers are advertised before the old are withdrawn",
            (
                GroupSpec("G1", _hosts(1, 5), (("Router(c)", SERVICE_PREFIX),)),
                GroupSpec("G2", _hosts(6, 10), (("Router(a)", DEFAULT),)),
            ),
        ),
        Scenario(
            "overlap",
            "host04-07 are in both groups: service via Router(a), everything else via Router(b)",
            (
                GroupSpec("G1", _hosts(1, 7), (("Router(a)", SERVICE_PREFIX),)),
                GroupSpec("G2", _hosts(4, 10), (("Router(b)", DEFAULT),)),
            ),
        ),
        Scenario(
            "merge",
            "host03-05 get two rules for the same router, merged into one policy RA",
            (
                GroupSpec("G1", _hosts(1, 5), (("Router(a)", SERVICE_PREFIX),)),
                GroupSpec("G2", _hosts(3, 5), (("Router(a)", DEFAULT),)),
            ),
        ),
    )
}


def get(name: str) -> Scenario:
    """Return the scenario with the given name."""
    return SCENARIOS[name]


def apply(lab: Lab, scenario: Scenario) -> dict[str, Any]:
    """Replace the controller's rules and groups with the scenario and apply it.

    The controller reconciles against what the routers are running, not
    against its previous database content, so wiping and recreating the
    records still produces a minimal, make-before-break transition.
    """
    topology = lab.topology()
    routers = {r["name"]: r for r in lab.api("GET", "/routers")}

    for group in lab.api("GET", "/groups"):
        lab.api("DELETE", f"/groups/{group['id']}")
    for rule in lab.api("GET", "/rules"):
        lab.api("DELETE", f"/rules/{rule['id']}")

    for spec in scenario.groups:
        rule_ids = []
        for router_name, prefix in spec.rules:
            rule = lab.api("POST", "/rules", {
                "comment": f"{spec.name}: {prefix} via {router_name}",
                "nexthop": routers[router_name]["address"],
                "entries": [{"type": "ipv6", "value": prefix}],
            })
            rule_ids.append(rule["id"])
        members = [topology.hosts[n - 1].ll for n in spec.hosts]
        group = lab.api("POST", "/groups", {"name": spec.name, "members": members})
        lab.api("PUT", f"/groups/{group['id']}/rules", {"rules": rule_ids})

    return lab.api("POST", "/policy/apply")


def print_result(scenario: Scenario, result: dict[str, Any]) -> None:
    """Print what the controller changed on each router."""
    for group in scenario.groups:
        print(f"  {group.describe()}")
    for r in result["results"]:
        if not r["success"]:
            print(f"  {r['router']}: FAILED — {r.get('error', '')}")
            continue
        changes = f"+{r['added']} added, ~{r['updated']} updated, -{r['deleted']} deleted, ={r['unchanged']} unchanged"
        print(f"  {r['router']}: {changes}; withdrawal RA to {r['withdrawn']} host(s)")
    for warning in result.get("warnings") or []:
        print(f"  warning: {warning}")
