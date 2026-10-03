"""TCP: which ports listen on the recorder, and what they say to a few opening lines."""
from __future__ import annotations

import asyncio
import errno
import time

from ..util import IS_WINDOWS, parse_ports, preview
from . import check

# Errors that mean we ran out of something locally, so a port's state is unknown, not closed.
_RESOURCE = {errno.EMFILE, errno.ENFILE, errno.ENOBUFS, errno.EADDRNOTAVAIL, 10048, 10055}


async def probe_port(host: str, port: int, timeout: float) -> str:
    """"open", "closed" (refused), "filtered" (no answer) or "error:<errno>"."""
    try:
        _, writer = await asyncio.wait_for(asyncio.open_connection(host, port), timeout)
    except asyncio.TimeoutError:
        return "filtered"
    except ConnectionRefusedError:
        return "closed"
    except OSError as e:
        return f"error:{e.errno or 0}"
    writer.close()
    try:
        await writer.wait_closed()
    except OSError:
        pass
    return "open"


@check("tcp-scan", phases=("ready", "begin"), help="TCP connect sweep (--tcp-ports)")
async def tcp_scan(ctx):
    ports = parse_ports(ctx.args.tcp_ports)
    found: dict[str, list[int]] = {}
    queue = iter(ports)
    done = 0
    started = time.monotonic()
    step = max(1, len(ports) // 10)

    async def worker():
        nonlocal done
        for port in queue:
            state = await probe_port(ctx.host, port, ctx.args.tcp_timeout)
            found.setdefault(state, []).append(port)
            if state == "open":
                ctx.log("tcp-scan", f"port {port} open")
            done += 1
            if done % step == 0:
                ctx.log("tcp-scan", f"{done}/{len(ports)} ports, {time.monotonic() - started:.0f}s")

    await asyncio.gather(*(worker() for _ in range(min(ctx.args.tcp_concurrency, len(ports)))))
    open_ports = sorted(found.get("open", []))
    errors = {k: len(v) for k, v in found.items() if k.startswith("error:")}
    exhausted = sum(n for k, n in errors.items() if int(k[6:]) in _RESOURCE)
    ctx.shared.setdefault("open_tcp", set()).update(open_ports)
    ctx.log("tcp-scan", f"open: {open_ports or 'none'}")
    return {
        "open": open_ports,
        "closed": len(found.get("closed", [])),
        "filtered": len(found.get("filtered", [])),
        "errors": errors,
        # A sweep that ran out of sockets must not read as "nothing listens".
        "reliable": exhausted == 0,
        "seconds": round(time.monotonic() - started, 1),
        "note": "Windows retries refused connects, so closed ports often show as filtered there"
        if IS_WINDOWS else None,
    }


def _range_for_staged(ctx) -> bytes:
    day, _, stamp = (ctx.report.meta.get("staged") or "/").partition("/")
    return f"RANGE {day}/{stamp} bytes=0-1023\r\n".encode()


# Opening lines tried on every open port, one connection each. Add guesses here; a callable
# gets the Context and returns the bytes.
PAYLOADS = [
    ("listen", b""),  # say nothing: does the device talk first?
    ("crlf", b"\r\n"),
    ("http-get", lambda ctx: f"GET / HTTP/1.1\r\nHost: {ctx.host}\r\nConnection: close\r\n\r\n".encode()),
    ("range-request", b"RANGE request bytes=0-\r\n"),
    ("range-bytes", b"RANGE bytes=0-1023\r\n"),
    ("range-staged", _range_for_staged),
    ("zeros", b"\0" * 16),
]


async def exchange(host: str, port: int, payload: bytes, wait: float = 2.0) -> dict:
    try:
        reader, writer = await asyncio.wait_for(asyncio.open_connection(host, port), 3)
    except (OSError, asyncio.TimeoutError) as e:
        return {"error": f"connect: {type(e).__name__}: {e}"}
    data = b""
    closed = False
    try:
        if payload:
            writer.write(payload)
            await writer.drain()
        end = time.monotonic() + wait
        while len(data) < 65536 and (left := end - time.monotonic()) > 0:
            try:
                chunk = await asyncio.wait_for(reader.read(4096), left)
            except asyncio.TimeoutError:
                break
            if not chunk:
                closed = True
                break
            data += chunk
    except OSError as e:
        return {"error": f"{type(e).__name__}: {e}", "received": preview(data)}
    finally:
        writer.close()
    return {"received": preview(data), "closed_by_device": closed}


@check("tcp-probe", phases=("ready", "begin"),
       help="send each PAYLOADS opening line to every open TCP port and record the answer")
async def tcp_probe(ctx):
    ports = sorted(ctx.shared.get("open_tcp", set()) | {53})
    out = {}
    for port in ports:
        for name, payload in PAYLOADS:
            data = payload(ctx) if callable(payload) else payload
            res = await exchange(ctx.host, port, data)
            if res.get("received", {}).get("length"):
                ctx.log("tcp-probe", f"{port} answered '{name}' with {res['received']['length']} bytes")
            out[f"{port}/{name}"] = res
    return out
