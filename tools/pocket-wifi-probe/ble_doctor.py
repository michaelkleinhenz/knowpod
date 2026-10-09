#!/usr/bin/env python3
"""Low-level Bluetooth check for a Pocket recorder, to see what bleak/BlueZ actually does.

Runs three steps and prints full detail, so we can tell a scan problem (bleak's discovery not
finding the recorder, e.g. a stray `bluetoothctl scan on`, or a BlueZ/D-Bus issue) from a
connect problem (the recorder refusing this machine). Needs only bleak; no root.

    python3 ble_doctor.py                       # scan, then connect (full check)
    python3 ble_doctor.py F4:4B:26:17:34:1D     # or pass an address
    python3 ble_doctor.py --connect-only        # connect straight away, no scan first

A connect that hangs in the full check but works with --connect-only means a scan was still
running on the adapter (BlueZ can't connect while scanning): close any `bluetoothctl scan on`.
"""
from __future__ import annotations

import asyncio
import sys
import traceback
from importlib.metadata import PackageNotFoundError, version

from bleak import BleakClient, BleakScanner

DEFAULT_ADDR = "F4:4B:26:17:34:1D"


def bleak_version() -> str:
    try:
        return version("bleak")
    except PackageNotFoundError:
        return "unknown"


async def connect(addr: str) -> None:
    print("\n--- direct connect by address (20s) ---")
    try:
        client = BleakClient(addr, timeout=20)
        await client.connect()
        print(f"  connected: {client.is_connected}")
        try:
            for s in client.services:
                print(f"  service {s.uuid}")
                for c in s.characteristics:
                    print(f"    char {c.uuid}  {sorted(c.properties)}")
        finally:
            await client.disconnect()
            print("  disconnected")
    except Exception:
        traceback.print_exc()


async def scan(addr: str) -> None:
    print("--- discover(10s): everything bleak sees ---")
    try:
        devices = await BleakScanner.discover(timeout=10)
        for d in devices:
            mark = "  <-- target" if d.address.upper() == addr.upper() else ""
            print(f"  {d.address}  {d.name!r}{mark}")
        print(f"  ({len(devices)} devices)")
    except Exception:
        traceback.print_exc()

    print("\n--- find_device_by_address(10s) ---")
    try:
        d = await BleakScanner.find_device_by_address(addr, timeout=10)
        print(f"  -> {d}")
    except Exception:
        traceback.print_exc()


async def main(addr: str, connect_only: bool) -> None:
    print(f"bleak {bleak_version()}; target {addr}"
          f"{'; connect-only (no scan)' if connect_only else ''}\n")
    if not connect_only:
        await scan(addr)
    await connect(addr)


if __name__ == "__main__":
    args = [a for a in sys.argv[1:]]
    connect_only = "--connect-only" in args
    args = [a for a in args if not a.startswith("--")]
    address = args[0] if args else DEFAULT_ADDR
    try:
        asyncio.run(main(address, connect_only))
    except KeyboardInterrupt:
        pass
