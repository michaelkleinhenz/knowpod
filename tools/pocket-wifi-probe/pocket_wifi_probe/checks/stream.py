"""The recorder's transfer socket: TCP 8475 on firmware 1.8 / WiFi firmware V9.

The first probe run found the port open while the AP was ready, silent when connected to, and
gone after one connection that sent bytes it didn't like. The app's strings speak of a "Pocket
Wi-Fi framed stream", which suggests the recorder streams frames once it's told to start.
So: connect first, without sending anything, then send APP&U&WIFI over Bluetooth and record
whatever arrives. It runs before every other check so that nothing has touched the port yet.
"""
from __future__ import annotations

import asyncio
import struct
import time
from pathlib import Path

from ..util import preview
from . import check


async def _read(reader, chunks, ctx, idle: float, limit: float) -> str:
    """Reads into chunks until idle seconds pass without data, limit seconds pass in all, or the
    recorder closes; returns which."""
    end = time.monotonic() + limit
    quiet_since = time.monotonic()
    while (left := end - time.monotonic()) > 0:
        try:
            chunk = await asyncio.wait_for(reader.read(65536), min(idle, left))
        except asyncio.TimeoutError:
            # A whole idle window without data is "idle", even if it also hit the limit.
            return "idle" if time.monotonic() - quiet_since >= idle * 0.99 else "time limit"
        except OSError as e:
            return f"error: {type(e).__name__}: {e}"
        if not chunk:
            return "closed"
        if not chunks:
            ctx.log("stream", "first bytes arrived")
        chunks.append((ctx.report.elapsed(), chunk))
        quiet_since = time.monotonic()
    return "time limit"


def length_fields(data: bytes, known: dict[str, int]) -> list[dict]:
    """Integers in the first 16 bytes that equal a known size: candidate header fields."""
    hits = []
    for off in range(16):
        for fmt, name in (("<H", "u16le"), (">H", "u16be"), ("<I", "u32le"), (">I", "u32be")):
            if off + struct.calcsize(fmt) > len(data):
                continue
            value = struct.unpack_from(fmt, data, off)[0]
            for what, size in known.items():
                if value and value == size:
                    hits.append({"offset": off, "type": name, "value": value, "equals": what})
    return hits


def describe(data: bytes, staged: int | None) -> dict:
    known = {"total received": len(data)}
    if staged:
        known["staged file size"] = staged
    sync = [i for i in range(len(data) - 1) if data[i] == 0xFF and data[i + 1] & 0xE0 == 0xE0]
    return {
        "length": len(data),
        "head": preview(data, 512),
        "tail": preview(data[-128:], 128),
        # MP3 frames start with 11 set bits; where the audio sits inside the frames.
        "mp3_sync_offsets": sync[:20],
        "length_fields": length_fields(data, known),
    }


@check("stream", phases=("ready",), priority=10,
       help="connect to --stream-port, then send APP&U&WIFI and record what the recorder streams")
async def stream(ctx):
    port = ctx.args.stream_port
    try:
        reader, writer = await asyncio.wait_for(asyncio.open_connection(ctx.host, port), 3)
    except (OSError, asyncio.TimeoutError) as e:
        return {"connected": False, "error": f"{type(e).__name__}: {e}"}
    ctx.log("stream", f"connected to {port}; listening before triggering")
    chunks: list[tuple[float, bytes]] = []
    out: dict = {"connected": True}
    try:
        out["before_trigger"] = await _read(reader, chunks, ctx, idle=2, limit=2)
        out["bytes_before_trigger"] = sum(len(c) for _, c in chunks)
        if out["before_trigger"] in ("idle", "time limit"):
            since = await ctx.pocket.send("U&WIFI")
            out["after_trigger"] = await _read(reader, chunks, ctx, idle=ctx.args.stream_idle,
                                               limit=ctx.args.stream_max)
            out["ble_after_trigger"] = [m.text for m in ctx.pocket.messages[since:]]
    finally:
        writer.close()
    data = b"".join(c for _, c in chunks)
    ctx.log("stream", f"{len(data)} bytes in {len(chunks)} reads, ended: "
                      f"{out.get('after_trigger', out['before_trigger'])}")
    out["reads"] = [{"t": t, "length": len(c)} for t, c in chunks]
    out["data"] = describe(data, ctx.report.meta.get("staged_bytes"))
    if data:
        path = Path(ctx.args.out).with_suffix(f".{port}.bin")
        path.write_bytes(data)
        out["saved_to"] = str(path)
    return out
