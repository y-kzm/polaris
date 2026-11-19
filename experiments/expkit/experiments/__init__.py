"""The experiments, in the order `exp run all` executes them."""

from . import failover, forwarding, load, overwrite, spread, transition, verify

EXPERIMENTS = {
    "verify": verify,
    "forwarding": forwarding,
    "load": load,
    "spread": spread,
    "overwrite": overwrite,
    "transition": transition,
    "failover": failover,
}

__all__ = ["EXPERIMENTS"]
