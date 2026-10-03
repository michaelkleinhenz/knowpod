"""Small helpers shared by the probe and its checks."""
from __future__ import annotations

import asyncio
import socket
import sys
from dataclasses import dataclass

IS_WINDOWS = sys.platform == "win32"


@dataclass
class Result:
    code: int
    out: str
    err: str

    @property
    def ok(self) -> bool:
        return self.code == 0

    def text(self) -> str:
        return (self.out + ("\n" + self.err if self.err.strip() else "")).strip()


def decode(data: bytes) -> str:
    # Windows console tools (netsh, ipconfig) write in the OEM code page.
    return data.decode("oem" if IS_WINDOWS else "utf-8", errors="replace")


async def run(*args: str, timeout: float = 30.0) -> Result:
    """Runs a program and returns its exit code and output; never raises for a failed program."""
    try:
        proc = await asyncio.create_subprocess_exec(
            *args, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
    except FileNotFoundError:
        return Result(127, "", f"{args[0]}: not found")
    try:
        out, err = await asyncio.wait_for(proc.communicate(), timeout)
    except asyncio.TimeoutError:
        proc.kill()
        out, err = await proc.communicate()
        return Result(124, decode(out), decode(err) + f"\n{args[0]}: timed out after {timeout:g}s")
    except BaseException:  # cancelled (Ctrl-C): don't leave the program running
        proc.kill()
        raise
    return Result(proc.returncode, decode(out), decode(err))


def local_address_for(host: str) -> str | None:
    """The address this machine would use to reach host (no packet is sent)."""
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.connect((host, 9))
            return s.getsockname()[0]
    except OSError:
        return None


def same_subnet24(a: str | None, b: str) -> bool:
    return bool(a) and a.rsplit(".", 1)[0] == b.rsplit(".", 1)[0]


def parse_ports(spec: str) -> list[int]:
    """"80,443,1000-1010" -> [80, 443, 1000, ..., 1010], de-duplicated, in the given order."""
    ports: list[int] = []
    seen = set()
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        if "-" in part:
            lo, hi = (int(x) for x in part.split("-", 1))
            rng = range(lo, hi + 1)
        else:
            rng = [int(part)]
        for p in rng:
            if not 1 <= p <= 65535:
                raise ValueError(f"port {p} out of range")
            if p not in seen:
                seen.add(p)
                ports.append(p)
    return ports


def preview(data: bytes, limit: int = 256) -> dict:
    """Bytes as hex and as printable text, for the report."""
    cut = data[:limit]
    return {
        "length": len(data),
        "hex": cut.hex(),
        "ascii": "".join(chr(b) if 32 <= b < 127 else "." for b in cut),
    }


def raise_fd_limit() -> None:
    """Port sweeps open many sockets at once; lift the soft limit where the OS has one."""
    try:
        import resource
    except ImportError:
        return
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    target = hard if hard != resource.RLIM_INFINITY else 65536
    if soft < target:
        try:
            resource.setrlimit(resource.RLIMIT_NOFILE, (target, hard))
        except (ValueError, OSError):
            pass
