"""The recorder's transfer socket: TCP 8475 on firmware 1.8 / WiFi firmware V9.

The first probe run found the port open while the AP was ready, silent when connected to, and
gone after one connection that sent bytes it didn't like. The app's strings speak of a "Pocket
Wi-Fi framed stream" and of "switching file transfer to Wi-Fi" (APP&WIFI&SWITCH), stopping
"the Bluetooth file transfer for Wi-Fi handoff". So: connect first, without sending anything,
then start a Bluetooth transfer of a long recording and switch it to WiFi (--stream-trigger),
and record whatever arrives. It runs before every other check so nothing has touched the port.
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


def recording_for(ctx) -> tuple[str, str]:
    """--stream-recording, or the longest recording, so a Bluetooth transfer of it is still
    running when it gets switched to WiFi."""
    from ..session import parse_recording
    if ctx.args.stream_recording:
        return parse_recording(ctx.args.stream_recording)
    recordings = ctx.shared.get("recordings") or []
    if recordings:
        day, ts, _ = max(recordings, key=lambda r: r[2])
        return day, ts
    day, _, ts = ctx.report.meta.get("staged", "/").partition("/")
    return day, ts


@check("stream", phases=("ready",), priority=10,
       help="connect to --stream-port, send --stream-trigger over Bluetooth, record what the recorder streams")
async def stream(ctx):
    port = ctx.args.stream_port
    # A refused connect costs nothing (the recorder answers with a reset), so keep trying until
    # the socket opens; a connection that gets in is never closed early or sent stray bytes.
    started = time.monotonic()
    attempts, last_error = 0, None
    while True:
        attempts += 1
        try:
            reader, writer = await asyncio.wait_for(asyncio.open_connection(ctx.host, port), 3)
            break
        except (OSError, asyncio.TimeoutError) as e:
            last_error = f"{type(e).__name__}: {e}"
        if time.monotonic() - started >= ctx.args.stream_wait:
            ctx.log("stream", f"{port} never opened in {ctx.args.stream_wait:g}s ({attempts} tries)")
            return {"connected": False, "attempts": attempts, "error": last_error}
        await asyncio.sleep(0.5)
    waited = round(time.monotonic() - started, 2)
    ctx.log("stream", f"connected to {port} after {waited}s ({attempts} tries); listening before triggering")
    chunks: list[tuple[float, bytes]] = []
    out: dict = {"connected": True, "attempts": attempts, "waited": waited}
    try:
        out["before_trigger"] = await _read(reader, chunks, ctx, idle=2, limit=2)
        out["bytes_before_trigger"] = sum(len(c) for _, c in chunks)
        if out["before_trigger"] in ("idle", "time limit"):
            day, ts = recording_for(ctx)
            out["recording"] = f"{day}/{ts}"
            since = ctx.pocket.mark()
            for command in (c.strip() for c in ctx.args.stream_trigger.split(",")):
                if command:
                    await ctx.pocket.send(command.format(date=day, ts=ts))
                    await asyncio.sleep(ctx.args.stream_gap)
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
