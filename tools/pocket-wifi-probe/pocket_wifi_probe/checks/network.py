"""The link to the recorder as the OS sees it."""
from __future__ import annotations

from ..util import IS_WINDOWS, run
from . import check


@check("net-info", phases=("ready", "begin"), help="address, DHCP lease, routes, neighbours (ARP)")
async def net_info(ctx):
    return {"local_ip": ctx.wifi.on_ap(), **await ctx.wifi.info()}


@check("ping", phases=("ready", "begin"), help="ICMP echo to the recorder")
async def ping(ctx):
    args = ["ping", "-n", "3", "-w", "1000", ctx.host] if IS_WINDOWS else ["ping", "-c", "3", "-W", "1", ctx.host]
    r = await run(*args, timeout=15)
    return {"exit": r.code, "output": r.text()}
