"""Where commands run: this machine or an SSH target, optionally inside a netns.

Single mode and testbed mode differ only here. Every script is passed on
stdin to `sh -s`, so quoting is the same locally and over SSH.
"""

from __future__ import annotations

import shlex
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

from .config import ExpError

SSH_OPTS = (
    "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
    "-o", "ControlMaster=auto", "-o", "ControlPersist=120", "-o", "ControlPath=~/.ssh/px-%C",
)


class Machine:
    """A machine that runs commands as root: local, or reached over SSH with sudo."""

    def __init__(self, name: str, ssh: str, workdir: str) -> None:
        self.name = name
        self.ssh = ssh
        self.workdir = workdir

    def _argv(self, command: str | None = None) -> list[str]:
        """Return argv for `sh -s` (command None) or for one shell command."""
        if self.ssh:
            remote = "sudo sh -s" if command is None else f"sudo sh -c {shlex.quote(command)}"
            return ["ssh", *SSH_OPTS, self.ssh, remote]
        return ["sh", "-s"] if command is None else ["sh", "-c", command]

    def sh(self, script: str, ns: str = "", check: bool = True, timeout: float = 600) -> str:
        """Run a shell script and return its stdout."""
        body = f"set -eu\n{script}\n"
        if ns:
            body = f"ip netns exec {ns} sh -s <<'PX_SCRIPT_EOF'\n{body}PX_SCRIPT_EOF\n"
        try:
            done = subprocess.run(self._argv(), input=body, text=True, capture_output=True, timeout=timeout, check=False)
        except subprocess.TimeoutExpired as err:
            raise ExpError(f"{self.name}: command timed out after {timeout:.0f} s") from err
        except FileNotFoundError as err:
            raise ExpError(f"{self.name}: {err}") from err
        if check and done.returncode != 0:
            where = f"{self.name}{'/' + ns if ns else ''}"
            detail = (done.stderr.strip() or done.stdout.strip())[-800:]
            raise ExpError(f"{where}: command failed (exit {done.returncode})\n{detail}")
        return done.stdout

    # ── background processes ────────────────────────────────────────────────
    def spawn(self, name: str, command: str, ns: str = "") -> None:
        """Start a background process; its output goes to <workdir>/proc/<name>.out|.err."""
        self.stop(name, "KILL", wait_s=1)  # a leftover of an interrupted run would otherwise be orphaned
        inner = shlex.quote(f"exec {command}")
        run = f"ip netns exec {ns} sh -c {inner}" if ns else f"sh -c {inner}"
        base = f"{self.workdir}/proc/{name}"
        self.sh(
            f"mkdir -p {self.workdir}/proc\n"
            f"nohup {run} > {base}.out 2> {base}.err < /dev/null &\n"
            f"echo $! > {base}.pid"
        )

    def stop(self, name: str, signal: str = "TERM", wait_s: float = 5.0) -> None:
        """Signal a spawned process and wait until it is gone."""
        base = f"{self.workdir}/proc/{name}"
        self.sh(
            f"[ -f {base}.pid ] || exit 0\n"
            f"pid=$(cat {base}.pid)\n"
            f"{self.ours('$pid')} || {{ rm -f {base}.pid; exit 0; }}\n"
            f"kill -{signal} $pid 2>/dev/null || true\n"
            f"n=0\n"
            f"while kill -0 $pid 2>/dev/null && [ $n -lt {int(wait_s * 10)} ]; do sleep 0.1; n=$((n+1)); done\n"
            f"kill -9 $pid 2>/dev/null || true\n"
            f"rm -f {base}.pid"
        )

    def ours(self, pid: str) -> str:
        """Return a shell test that a PID is still a process this tool started.

        PIDs are reused; after a reboot a stale pid file may name anything.
        """
        return f"tr '\\0' ' ' < /proc/{pid}/cmdline 2>/dev/null | grep -q -e {shlex.quote(self.workdir)} -e tcpdump"

    def alive(self, name: str) -> bool:
        """Report whether a spawned process is still running."""
        base = f"{self.workdir}/proc/{name}"
        return self.sh(f"[ -f {base}.pid ] && kill -0 $(cat {base}.pid) 2>/dev/null && echo yes || true").strip() == "yes"

    def output(self, name: str, stream: str = "out") -> str:
        """Return what a spawned process has written so far."""
        return self.sh(f"cat {self.workdir}/proc/{name}.{stream} 2>/dev/null || true")

    # ── files ───────────────────────────────────────────────────────────────
    def write(self, remote: str, text: str) -> None:
        """Create a file with the given content."""
        self._pipe_in(f"mkdir -p {shlex.quote(str(Path(remote).parent))} && cat > {shlex.quote(remote)}", text.encode())

    def read(self, remote: str) -> str:
        """Return the content of a file ("" if it does not exist)."""
        return self.sh(f"cat {shlex.quote(remote)} 2>/dev/null || true")

    def put_tree(self, local: Path, remote: str, excludes: tuple[str, ...] = ()) -> None:
        """Copy a directory to the machine."""
        flags = " ".join(f"--exclude={shlex.quote(e)}" for e in excludes)
        pack = subprocess.run(
            f"tar -C {shlex.quote(str(local))} {flags} -cf - .", shell=True, capture_output=True, check=False
        )
        if pack.returncode != 0:
            raise ExpError(f"cannot pack {local}: {pack.stderr.decode(errors='replace')[-300:]}")
        target = shlex.quote(remote)
        self._pipe_in(f"rm -rf {target} && mkdir -p {target} && tar -C {target} -xf -", pack.stdout)

    def get_tree(self, remote: str, local: Path) -> None:
        """Copy a directory from the machine into `local`."""
        local.mkdir(parents=True, exist_ok=True)
        if not self.ssh and Path(remote).is_dir():
            shutil.copytree(remote, local, dirs_exist_ok=True)
            return
        pack = subprocess.run(
            self._argv(f"tar -C {shlex.quote(remote)} -cf - ."), capture_output=True, check=False
        )
        if pack.returncode != 0:
            raise ExpError(f"{self.name}: cannot fetch {remote}: {pack.stderr.decode(errors='replace')[-300:]}")
        subprocess.run(["tar", "-C", str(local), "-xf", "-"], input=pack.stdout, check=True)

    def _pipe_in(self, command: str, data: bytes) -> None:
        done = subprocess.run(self._argv(command), input=data, capture_output=True, check=False)
        if done.returncode != 0:
            raise ExpError(f"{self.name}: {command[:60]}… failed: {done.stderr.decode(errors='replace')[-300:]}")


@dataclass(frozen=True)
class Node:
    """A network node: a machine, or one network namespace on a machine."""

    machine: Machine
    ns: str = ""

    @property
    def label(self) -> str:
        """Name used for process and file names on the machine."""
        return self.ns or self.machine.name

    def sh(self, script: str, check: bool = True, timeout: float = 600) -> str:
        """Run a shell script on the node."""
        return self.machine.sh(script, ns=self.ns, check=check, timeout=timeout)

    def spawn(self, name: str, command: str) -> None:
        """Start a background process on the node."""
        self.machine.spawn(name, command, ns=self.ns)

    def now(self) -> float:
        """Return the node's clock (seconds since the epoch)."""
        return float(self.machine.sh("date +%s.%N").strip())
