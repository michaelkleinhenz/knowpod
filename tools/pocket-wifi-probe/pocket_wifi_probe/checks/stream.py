"""The recorder's transfer socket: TCP 8475 on firmware 1.8 / WiFi firmware V9.

The port is open while the AP is ready, and silent when connected to. The official app (seen in
the phone's HCI log) starts a Bluetooth transfer with APP&U&<date>&<ts> and 0.35 s later sends
APP&U&WIFI; the recorder stops the Bluetooth stream, answers MCU&U&WIFI and MCU&U&<size>, and
sends the file over WiFi, ending with MCU&OFF. So: connect first, without sending anything, then
send that pair (--stream-trigger) for a long recording, and record whatever arrives. It runs
before every other check so nothing has touched the port.
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


async def cycle_ap(ctx) -> dict:
    """WIFIC, leave, WIFIO, rejoin, wait for WIFIS=1: does a restarted AP serve more files?
    WIFIO comes straight after WIFIC, as the official app raises the AP (WIFIS, WIFIO)."""
    started = time.monotonic()
    pocket, wifi, args = ctx.pocket, ctx.wifi, ctx.args
    status = ctx.shared["wifi_status"]
    closed = await pocket.request("WIFIC", "WIFIC", timeout=5)
    await wifi.leave()
    await asyncio.sleep(2)
    status.ready.clear()
    status.value = None
    raised_at = time.monotonic()
    opened = await pocket.request("WIFIO", "WIFIO", timeout=5)
    joined = await wifi.join(ctx.ssid, raised_at + args.join_timeout)
    ready = False
    if joined:
        try:
            await asyncio.wait_for(status.ready.wait(), max(0.1, raised_at + args.ready_timeout - time.monotonic()))
            ready = True
        except asyncio.TimeoutError:
            pass
    secs = round(time.monotonic() - started, 1)
    ctx.log("stream", f"cycle: WIFIC {'ok' if closed is not None else 'unanswered'}, WIFIO "
                      f"{'ok' if opened is not None else 'unanswered'}, joined={joined}, WIFIS=1 {ready}, {secs}s")
    return {"cycle": {"wific": closed is not None, "wifio": opened is not None, "joined": joined,
                      "ready": ready, "seconds": secs}}


async def _connect(ctx, port: int) -> tuple:
    """Connects to the transfer socket, retrying for --stream-wait seconds. A refused connect
    costs nothing (the recorder answers with a reset); a connection that gets in is never closed
    early or sent stray bytes. Returns (reader, writer, attempts, waited, error)."""
    started = time.monotonic()
    attempts, last_error = 0, None
    while True:
        attempts += 1
        try:
            reader, writer = await asyncio.wait_for(asyncio.open_connection(ctx.host, port), 3)
            return reader, writer, attempts, round(time.monotonic() - started, 2), None
        except (OSError, asyncio.TimeoutError) as e:
            last_error = f"{type(e).__name__}: {e}"
        if time.monotonic() - started >= ctx.args.stream_wait:
            return None, None, attempts, round(time.monotonic() - started, 2), last_error
        await asyncio.sleep(0.5)


@check("stream", phases=("ready",), priority=10,
       help="connect to --stream-port, send --stream-trigger over Bluetooth, record what the recorder streams")
async def stream(ctx):
    port = ctx.args.stream_port
    reader, writer, attempts, waited, error = await _connect(ctx, port)
    if reader is None:
        ctx.log("stream", f"{port} never opened in {ctx.args.stream_wait:g}s ({attempts} tries)")
        return {"connected": False, "attempts": attempts, "error": error}
    ctx.log("stream", f"connected to {port} after {waited}s ({attempts} tries); listening before triggering")
    chunks: list[tuple[float, bytes]] = []
    # Every connection made (the first, plus one per "reconnect" step), each read on its own.
    conns = [{"opened": ctx.report.elapsed(), "writer": writer, "chunks": chunks, "task": None}]
    out: dict = {"connected": True, "attempts": attempts, "waited": waited}

    def start_reading(conn, reader) -> None:
        conn["task"] = asyncio.create_task(_read(reader, conn["chunks"], ctx, idle=ctx.args.stream_idle,
                                                 limit=ctx.args.stream_max))

    try:
        out["before_trigger"] = await _read(reader, chunks, ctx, idle=2, limit=2)
        out["bytes_before_trigger"] = sum(len(c) for _, c in chunks)
        if out["before_trigger"] in ("idle", "time limit"):
            day, ts = recording_for(ctx)
            out["recording"] = f"{day}/{ts}"
            since = ctx.pocket.mark()
            # Read while triggering: with several files, the recorder only finishes one (MCU&OFF)
            # if the socket is drained.
            start_reading(conns[0], reader)
            sent = since
            out["trigger_log"] = []
            for command in (c.strip() for c in ctx.args.stream_trigger.split(",")):
                if command.startswith("wait:"):  # wait:OFF = until MCU&OFF answers the last command
                    name = command[5:]
                    msg = await ctx.pocket.wait_for(lambda m: m.value(name) is not None, since=sent,
                                                    timeout=ctx.args.stream_max)
                    ctx.log("stream", f"{command}: {'got ' + msg.text if msg else 'timed out'}")
                    out["trigger_log"].append({"wait": name, "ok": msg is not None,
                                               "bytes_so_far": [sum(len(c) for _, c in k["chunks"])
                                                                for k in conns]})
                elif command.startswith("sleep:"):
                    await asyncio.sleep(float(command[6:]))
                elif command in ("close", "cycle"):  # close the connections made so far (keeping what they got)
                    for conn in conns:
                        if not conn.get("closed"):
                            conn["closed"] = ctx.report.elapsed()
                            conn["writer"].close()
                            try:
                                await conn["writer"].wait_closed()
                            except OSError:
                                pass
                    ctx.log("stream", f"closed {len(conns)} connection(s)")
                    out["trigger_log"].append({"close": len(conns)})
                    if command == "cycle":  # then restart the AP and rejoin it
                        out["trigger_log"].append(await cycle_ap(ctx))
                elif command == "reconnect":  # a new connection; earlier ones stay open and read
                    r, w, tries, secs, err = await _connect(ctx, port)
                    ctx.log("stream", f"reconnect: {'connected' if r else 'failed: ' + str(err)} "
                                      f"after {secs}s ({tries} tries)")
                    out["trigger_log"].append({"reconnect": r is not None, "tries": tries, "error": err})
                    if r is not None:
                        conns.append({"opened": ctx.report.elapsed(), "writer": w, "chunks": [], "task": None})
                        start_reading(conns[-1], r)
                elif command:
                    sent = await ctx.pocket.send(command.format(date=day, ts=ts))
                    await asyncio.sleep(ctx.args.stream_gap)
            out["after_trigger"] = await conns[0]["task"]
            for conn in conns[1:]:
                conn["ended"] = await conn["task"]
            out["ble_after_trigger"] = [m.text for m in ctx.pocket.messages[since:]]
    finally:
        for conn in conns:
            if conn["task"] and not conn["task"].done():
                conn["task"].cancel()
            conn["writer"].close()
    data = b"".join(c for _, c in chunks)
    ctx.log("stream", f"{len(data)} bytes in {len(chunks)} reads, ended: "
                      f"{out.get('after_trigger', out['before_trigger'])}")
    out["reads"] = [{"t": t, "length": len(c)} for t, c in chunks]
    out["data"] = describe(data, ctx.report.meta.get("staged_bytes"))
    if data:
        path = Path(ctx.args.out).with_suffix(f".{port}.bin")
        path.write_bytes(data)
        out["saved_to"] = str(path)
    if len(conns) > 1:
        out["connections"] = []
        for n, conn in enumerate(conns, 1):
            body = b"".join(c for _, c in conn["chunks"])
            info = {"opened": conn["opened"], "closed": conn.get("closed"), "bytes": len(body), "ended": conn.get("ended", out.get("after_trigger")),
                    "first_read": conn["chunks"][0][0] if conn["chunks"] else None}
            if body and n > 1:
                path = Path(ctx.args.out).with_suffix(f".{port}-{n}.bin")
                path.write_bytes(body)
                info["saved_to"] = str(path)
            out["connections"].append(info)
        ctx.log("stream", "per connection: " + ", ".join(f"#{n} {c['bytes']} bytes"
                                                         for n, c in enumerate(out["connections"], 1)))
    return out
