"""Record every packet on the WiFi interface while on the recorder's network."""
from __future__ import annotations

import asyncio
import shutil
import sys
from pathlib import Path

from ..util import decode
from . import monitor


@monitor("capture", help="tcpdump of the WiFi interface to a .pcap next to the report (Linux, needs root "
                         "or CAP_NET_RAW)")
async def capture(ctx, stop: asyncio.Event):
    if not sys.platform.startswith("linux"):
        return {"skipped": "only on Linux; on Windows run Wireshark (or pktmon) on the WiFi adapter by hand"}
    if not shutil.which("tcpdump"):
        return {"skipped": "tcpdump is not installed"}
    iface = getattr(ctx.wifi, "iface", None) or "any"
    path = Path(ctx.args.out).with_suffix(".pcap")
    proc = await asyncio.create_subprocess_exec(
        "tcpdump", "-i", iface, "-n", "-U", "-w", str(path),
        stdout=asyncio.subprocess.DEVNULL, stderr=asyncio.subprocess.PIPE)
    try:
        await asyncio.wait_for(proc.wait(), 1.5)
        # It quit straight away: usually no permission.
        return {"skipped": decode(await proc.stderr.read()).strip()}
    except asyncio.TimeoutError:
        pass
    ctx.log("capture", f"capturing {iface} to {path}")
    try:
        await stop.wait()
    finally:
        proc.terminate()
        try:
            await asyncio.wait_for(proc.wait(), 5)
        except asyncio.TimeoutError:
            proc.kill()
    return {"pcap": str(path), "tcpdump": decode(await proc.stderr.read()).strip()}
