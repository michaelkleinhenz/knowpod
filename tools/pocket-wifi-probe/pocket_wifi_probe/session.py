"""One probe run: unlock, raise the access point the firmware 1.8 way, join, run the checks,
and put everything back.

Firmware 1.8 order (pocket-libre PROTOCOL.md): U&WIFI (no answer) -> WIFI (SSID and password)
-> stage a recording with U&<date>&<timestamp> -> WIFIO. Staging before WIFIO is what keeps the
recorder's Bluetooth alive on 1.8; the other order strands it until it's power-cycled. The AP
then reports WIFIS 3 (up), 2 (client joining), 1 (ready), and the window to join is seconds.
"""
from __future__ import annotations

import asyncio
import re
import time
import traceback
from pathlib import Path

from . import checks
from .context import Context, Report
from .hostwifi import HostWifiError, backend
from .pocket import COMMAND, Message, PocketError, PocketLink
from .util import raise_fd_limit


class ProbeError(Exception):
    pass


class WifiStatus:
    """The last WIFIS value the recorder reported, asked for or not."""

    def __init__(self) -> None:
        self.value: int | None = None
        self.ready = asyncio.Event()
        self.history: list[tuple[float, int]] = []

    def update(self, msg: Message) -> None:
        v = msg.value("WIFIS")
        if v is None or not v.strip().isdigit():
            return
        n = int(v.strip())
        if n != self.value:
            self.history.append((msg.t, n))
        self.value = n
        if n == 1:
            self.ready.set()


async def probe(args) -> int:
    out = Path(args.out)
    report = Report(secrets=[args.key, args.key[:8]], verbose=not args.quiet)
    report.meta.update({
        "address": args.address, "host": args.host, "wifi_backend": args.wifi,
        "recording": args.recording, "begin": not args.no_begin, "force": args.force,
    })
    raise_fd_limit()
    selected = checks.select(args.checks, args.skip)
    report.meta["checks"] = [c.name for c in selected]

    status = WifiStatus()

    def on_message(m: Message) -> None:
        label = "ble<" if m.source == COMMAND else f"ble<{m.source[4:8]}"
        report.log(label, m.text, t=m.t)
        status.update(m)

    pocket = PocketLink(args.address, scan_timeout=args.scan_timeout, notify_all=args.notify_all,
                        on_message=on_message, on_event=report.log)
    wifi = backend(args.wifi, args.host, args.iface, report.log)
    ctx = Context(args=args, report=report, pocket=pocket, wifi=wifi, host=args.host,
                  out_dir=out.parent)
    ctx.shared["wifi_status"] = status
    code = 1
    try:
        async with pocket:
            await unlock(ctx)
            await run_phase(ctx, "ble", selected)
            await wifi_session(ctx, selected, status)
            code = 0
    except (PocketError, HostWifiError, ProbeError) as e:
        report.log("error", str(e))
    except (KeyboardInterrupt, asyncio.CancelledError):
        report.log("error", "interrupted")
        code = 130
    finally:
        report.meta["wifi_status_history"] = [(report.elapsed(t), v) for t, v in status.history]
        report.write(out)
        print(f"\nReport written to {out}")
    return code


async def unlock(ctx: Context) -> None:
    pocket = ctx.pocket
    if await pocket.request(f"SK&{ctx.args.key}", "SK") != "OK":
        raise ProbeError("The recorder refused the session key")
    fw = (await pocket.request("FW", "FW") or "").strip()
    ctx.report.meta["firmware"] = fw
    ctx.log("device", f"firmware {fw or 'unknown'}")
    if not fw.startswith("1.8"):
        msg = (f"Firmware is {fw or 'unknown'}, not 1.8. The WiFi sequence here is the firmware 1.8 one; "
               "on other firmware it may strand the recorder until it's power-cycled.")
        if not ctx.args.force:
            raise ProbeError(msg + " Use --force to try anyway.")
        ctx.log("warning", msg)


async def pick_recording(ctx: Context) -> tuple[str, str]:
    """The recording to stage: --recording DATE/TIMESTAMP, or the newest on the recorder."""
    if ctx.args.recording:
        m = re.fullmatch(r"(\d{4}-\d{2}-\d{2})/(\d{14})", ctx.args.recording)
        if not m:
            raise ProbeError("--recording must look like 2026-09-03/20260903145856")
        return m.group(1), m.group(2)
    pocket = ctx.pocket
    dirs = [m.value("DIRS") for m in await pocket.collect("LIST_DIRS", until="DIRS_SUM")]
    dirs = sorted(d for d in dirs if d)
    for day in reversed(dirs):
        rows = [m.value("F") for m in await pocket.collect(f"LIST&{day}", until="LIST")]
        files = sorted(r.split("&")[1] for r in rows if r and r.count("&") >= 2)
        if files:
            return day, files[-1]
    raise ProbeError("No recordings on the recorder; the 1.8 sequence needs one to stage")


async def wifi_session(ctx: Context, selected: list[checks.Check], status: WifiStatus) -> None:
    args, pocket, wifi, report = ctx.args, ctx.pocket, ctx.wifi, ctx.report
    day, stamp = await pick_recording(ctx)
    report.meta["staged"] = f"{day}/{stamp}"
    await wifi.setup()

    raised = prepared = False
    stop = asyncio.Event()
    background: list[asyncio.Task] = []
    try:
        # 1. Ask for WiFi mode. No answer is normal on 1.8; log whatever comes anyway.
        since = await pocket.send("U&WIFI")
        await pocket.wait_for(lambda m: False, since=since, timeout=1.0)

        # 2. Credentials.
        creds = await pocket.request("WIFI", "WIFI", timeout=5)
        if not creds or "&" not in creds:
            raise ProbeError(f"The recorder did not give WiFi credentials (got {creds!r})")
        ssid, password = creds.split("&", 1)
        report.secrets.append(password)
        ctx.ssid = ssid
        report.meta["ssid"] = ssid
        if password != args.key[:8]:
            ctx.log("note", "the WiFi password is not the first 8 characters of the session key")

        # 3. Stage the recording BEFORE raising the AP (the 1.8 order).
        size = await pocket.request(f"U&{day}&{stamp}", "U", timeout=10, accept=lambda v: v.strip().isdigit())
        if size is None:
            raise ProbeError("The recorder did not stage the recording; not raising the access point")
        report.meta["staged_bytes"] = int(size)

        # 4. Have the profile ready, then raise the AP and join while polling its status.
        await wifi.prepare(ssid, password)
        prepared = True
        raised = True
        raised_at = time.monotonic()
        await pocket.send("WIFIO")
        if args.status_interval > 0:
            background.append(asyncio.create_task(poll_status(ctx, args.status_interval, stop)))
        joined = await wifi.join(ssid, raised_at + args.join_timeout)
        if not joined:
            raise ProbeError(f"Could not join {ssid} within {args.join_timeout:g}s")
        ctx.local_ip = wifi.on_ap()
        report.meta["local_ip"] = ctx.local_ip
        ctx.log("wifi", f"on the recorder's network as {ctx.local_ip}")

        remaining = raised_at + args.ready_timeout - time.monotonic()
        try:
            await asyncio.wait_for(status.ready.wait(), max(0.1, remaining))
            ctx.log("device", "access point reports ready (WIFIS=1)")
        except asyncio.TimeoutError:
            ctx.log("warning", f"no WIFIS=1 within {args.ready_timeout:g}s (last {status.value}); probing anyway")

        # 5. Background monitors, then the checks.
        for m in (c for c in selected if c.kind == "monitor"):
            background.append(asyncio.create_task(run_monitor(ctx, m, stop)))
        await asyncio.sleep(0.5)  # let monitors start listening
        await run_phase(ctx, "ready", selected)

        if not args.no_begin:
            since = await pocket.send("U&WIFI")
            await pocket.wait_for(lambda m: False, since=since, timeout=args.begin_wait)
            await run_phase(ctx, "begin", selected)

        if args.hold > 0:
            ctx.log("hold", f"keeping the access point up for {args.hold:g}s")
            await asyncio.sleep(args.hold)
        if args.pause:
            await asyncio.to_thread(input, "\nOn the recorder's network. Press Enter to tear down... ")
    finally:
        await teardown(ctx, stop, background, raised=raised, prepared=prepared)


async def poll_status(ctx: Context, interval: float, stop: asyncio.Event) -> None:
    """Asks WIFIS every interval; the answers are tracked as they arrive."""
    while not stop.is_set():
        if ctx.pocket.connected:
            try:
                await ctx.pocket.send("WIFIS")
            except Exception as e:
                ctx.log("ble", f"status poll failed: {e}")
        try:
            await asyncio.wait_for(stop.wait(), interval)
        except asyncio.TimeoutError:
            pass


async def run_phase(ctx: Context, phase: str, selected: list[checks.Check]) -> None:
    ctx.phase = phase
    for c in selected:
        if c.kind != "check" or phase not in c.phases:
            continue
        if phase != "ble" and not ctx.wifi.on_ap():
            ctx.log("warning", f"no longer on the recorder's network before {c.name}")
        ctx.log("check", f"{c.name} [{phase}]")
        started = time.monotonic()
        try:
            value = await asyncio.wait_for(c.func(ctx), ctx.args.check_timeout)
            res = {"ok": True, "result": value}
        except asyncio.TimeoutError:
            res = {"ok": False, "error": f"timed out after {ctx.args.check_timeout:g}s"}
        except Exception as e:
            res = {"ok": False, "error": f"{type(e).__name__}: {e}", "traceback": traceback.format_exc()}
        res["seconds"] = round(time.monotonic() - started, 2)
        if not res["ok"]:
            ctx.log("check", f"{c.name} failed: {res['error']}")
        ctx.report.result(phase, c.name, res)


async def run_monitor(ctx: Context, m: checks.Check, stop: asyncio.Event) -> None:
    started = time.monotonic()
    try:
        res = {"ok": True, "result": await m.func(ctx, stop)}
    except Exception as e:
        res = {"ok": False, "error": f"{type(e).__name__}: {e}", "traceback": traceback.format_exc()}
        ctx.log("monitor", f"{m.name} failed: {res['error']}")
    res["seconds"] = round(time.monotonic() - started, 2)
    ctx.report.result("monitors", m.name, res)


async def teardown(ctx: Context, stop: asyncio.Event, background: list[asyncio.Task], *,
                   raised: bool, prepared: bool) -> None:
    """Every step runs even if an earlier one fails, and Ctrl-C can't skip it."""
    async def step(name, coro):
        try:
            await asyncio.shield(coro)
        except BaseException as e:  # keep tearing down
            ctx.log("teardown", f"{name} failed: {type(e).__name__}: {e}")

    ctx.log("teardown", "starting")
    stop.set()
    if background:
        await step("stopping monitors", asyncio.wait(background, timeout=15))
        for t in background:
            t.cancel()
    if raised:
        if ctx.pocket.connected:
            async def cleanup():
                since = await ctx.pocket.send("WIFIC")
                await ctx.pocket.wait_for(lambda m: False, since=since, timeout=2.0)
            await step("WIFIC", cleanup())
        else:
            ctx.log("warning", "Bluetooth is gone, so WIFIC could not be sent. If the recorder "
                               "no longer shows up over Bluetooth it needs a power-cycle.")
    if prepared:
        await step("restoring WiFi", ctx.wifi.restore())
    ctx.log("teardown", "done")
