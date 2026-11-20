"""Result files: one directory per run with raw data, CSV tables and a summary."""

from __future__ import annotations

import csv
import json
import statistics
import subprocess
import time
from dataclasses import asdict
from pathlib import Path
from typing import Any, Iterable, Sequence

from .config import EXP_DIR, REPO_DIR, Config
from .site import Site


def describe(values: Sequence[float]) -> dict[str, float | int | None]:
    """Return n, median, min, max, mean and standard deviation of a sample."""
    if not values:
        return {"n": 0, "median": None, "min": None, "max": None, "mean": None, "sd": None}
    return {
        "n": len(values), "median": statistics.median(values), "min": min(values), "max": max(values),
        "mean": statistics.fmean(values), "sd": statistics.stdev(values) if len(values) > 1 else 0.0,
    }


def ms(value: float | None, digits: int = 1) -> str:
    """Format seconds as milliseconds, or "–" when there is no value."""
    return "–" if value is None else f"{value * 1000:.{digits}f}"


def fmt(value: float | None, digits: int = 2) -> str:
    """Format a number, or "–" when there is no value."""
    return "–" if value is None else f"{value:.{digits}f}"


def _git(path: Path) -> str:
    done = subprocess.run(
        ["git", "-C", str(path), "describe", "--always", "--dirty"], capture_output=True, text=True, check=False
    )
    return done.stdout.strip() or "unknown"


class Run:
    """Output directory of one experiment run."""

    def __init__(self, cfg: Config, site: Site, name: str) -> None:
        self.cfg, self.site, self.name = cfg, site, name
        self.dir = EXP_DIR / "results" / f"{name}_{time.strftime('%Y%m%d_%H%M%S')}"
        (self.dir / "raw").mkdir(parents=True)
        self._summary: list[str] = [f"# {name}", ""]
        self.ok = True
        self._write_meta()

    def _write_meta(self) -> None:
        machines = {}
        for machine in self.site.machines():
            machines[machine.name] = {
                "kernel": machine.sh("uname -r").strip(),
                "os": machine.sh(". /etc/os-release 2>/dev/null && echo \"$PRETTY_NAME\" || true").strip(),
                "clock_offset_s": round(float(machine.sh("date +%s.%N").strip()) - time.time(), 4),
                "kernel_rio": machine.sh(
                    "[ -e /proc/sys/net/ipv6/conf/all/accept_ra_rt_info_max_plen ] && echo yes || echo no"
                ).strip(),
            }
        meta = {
            "experiment": self.name,
            "started": time.strftime("%Y-%m-%d %H:%M:%S %z"),
            "config_file": str(self.cfg.path),
            "mode": self.cfg.mode, "upstream": self.cfg.upstream,
            "hosts": self.cfg.hosts, "g1_hosts": self.cfg.g1_hosts,
            "single_address": self.cfg.single_address,
            "ra": asdict(self.site.ra),
            "experiment_settings": self.cfg.exp(self.name),
            "polaris": _git(REPO_DIR), "go_ra": _git(self.cfg.go_ra_dir),
            "machines": machines,
        }
        self.json("meta.json", meta)

    def path(self, name: str) -> Path:
        """Return a path inside the run directory."""
        return self.dir / name

    def json(self, name: str, data: Any) -> None:
        """Write a JSON file."""
        self.path(name).write_text(json.dumps(data, indent=2, default=str) + "\n")

    def csv(self, name: str, header: Sequence[str], rows: Iterable[Sequence[Any]]) -> None:
        """Write a CSV file."""
        with self.path(name).open("w", newline="") as f:
            writer = csv.writer(f)
            writer.writerow(header)
            writer.writerows(rows)

    def say(self, text: str = "") -> None:
        """Print a line and add it to summary.md."""
        print(text, flush=True)
        self._summary.append(text)

    def table(self, header: Sequence[str], rows: Iterable[Sequence[Any]]) -> None:
        """Print a table and add it to summary.md as Markdown."""
        rows = [[str(c) for c in row] for row in rows]
        widths = [max(len(str(h)), *(len(r[i]) for r in rows)) if rows else len(str(h)) for i, h in enumerate(header)]
        line = lambda cells: "| " + " | ".join(str(c).ljust(w) for c, w in zip(cells, widths)) + " |"  # noqa: E731
        self.say(line(header))
        self.say("|" + "|".join("-" * (w + 2) for w in widths) + "|")
        for row in rows:
            self.say(line(row))
        self.say()

    def fail(self, text: str) -> None:
        """Record that the run did not meet its expectation."""
        self.ok = False
        self.say(f"**FAIL**: {text}")

    def close(self) -> None:
        """Write summary.md."""
        self.say(f"Result: {'PASS' if self.ok else 'FAIL'}  —  files in {self.dir}")
        self.path("summary.md").write_text("\n".join(self._summary) + "\n")
