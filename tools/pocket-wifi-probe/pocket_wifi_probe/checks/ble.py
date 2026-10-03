"""What the recorder says about itself over Bluetooth."""
from __future__ import annotations

from ..util import preview
from . import check

INFO = [  # (command, answer)
    ("BAT", "BAT"),
    ("WF", "WF"),  # WiFi firmware version
    ("SPACE", "SPA"),
    ("STE", "STE"),
    ("GET&USB", "USB"),
]


@check("ble-info", phases=("ble",), help="battery, WiFi firmware, storage, state")
async def ble_info(ctx):
    out = {"firmware": ctx.report.meta.get("firmware")}
    for command, answer in INFO:
        out[command] = await ctx.pocket.request(command, answer)
    return out


@check("gatt", phases=("ble",), help="GATT services and characteristics, with readable values")
async def gatt(ctx):
    services = ctx.pocket.services()
    client = ctx.pocket.client
    for s in services:
        for c in s["characteristics"]:
            if "read" in c["properties"]:
                try:
                    c["value"] = preview(bytes(await client.read_gatt_char(c["handle"])))
                except Exception as e:
                    c["value_error"] = str(e)
    return services


@check("ble-state", phases=("ready", "begin"), help="WIFIS and STE, and the WIFIS history so far")
async def ble_state(ctx):
    status = ctx.shared["wifi_status"]
    return {
        "WIFIS": await ctx.pocket.request("WIFIS", "WIFIS"),
        "STE": await ctx.pocket.request("STE", "STE"),
        "connected": ctx.pocket.connected,
        "history": [(ctx.report.elapsed(t), v) for t, v in status.history],
    }
