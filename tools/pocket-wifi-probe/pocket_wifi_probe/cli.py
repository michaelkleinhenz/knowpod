"""Command line: pocket-wifi-probe <bluetooth-address> <session-key> [options]."""
from __future__ import annotations

import argparse
import asyncio
import datetime as dt
import os
import re
import sys

from . import checks
from .session import probe

DEFAULT_HOST = "192.168.200.1"


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="pocket-wifi-probe",
        description="Raise a Pocket recorder's WiFi access point (firmware 1.8 sequence), join it, "
                    "and run scans and tests to work out how the WiFi file transfer works. "
                    "Puts the recorder and this machine's WiFi back afterwards.")
    p.add_argument("address", nargs="?", help="the recorder's Bluetooth address, AA:BB:CC:DD:EE:FF")
    p.add_argument("key", nargs="?", help="the recorder's session key "
                   "(or set POCKET_SESSION_KEY to keep it out of your shell history)")
    p.add_argument("--list-checks", action="store_true", help="list the checks and monitors, then exit")
    p.add_argument("--checks", type=_names, help="run only these checks/monitors (comma-separated)")
    p.add_argument("--skip", type=_names, default=[], help="skip these checks/monitors (comma-separated)")
    p.add_argument("--out", help="report file (default pocket-probe-<time>.json)")
    p.add_argument("-q", "--quiet", action="store_true", help="don't print the timeline as it happens")

    g = p.add_argument_group("recorder")
    g.add_argument("--recording", help="recording to stage, DATE/TIMESTAMP (default: the newest)")
    g.add_argument("--host", default=DEFAULT_HOST, help=f"the recorder's address on its AP (default {DEFAULT_HOST})")
    g.add_argument("--force", action="store_true", help="run on firmware other than 1.8 (may strand the recorder)")
    g.add_argument("--no-begin", action="store_true", help="don't send APP&U&WIFI after the AP is ready, "
                   "so the 'begin' phase is skipped")
    g.add_argument("--begin-wait", type=float, default=3.0, help="seconds to wait after APP&U&WIFI (default 3)")
    g.add_argument("--notify-all", action="store_true",
                   help="after unlocking, also subscribe to and log every other notify characteristic "
                        "(the recorder dropped the link while ffd2 was subscribing; prefer --notify)")
    g.add_argument("--notify", type=_names, default=["001120a1"],
                   help="after unlocking, also subscribe to these characteristics (comma-separated UUIDs "
                        "or prefixes; default 001120a1, Bluetooth audio: without a subscriber the recorder "
                        "ends a Bluetooth transfer at once, so there's nothing to switch to WiFi)")
    g.add_argument("--scan-timeout", type=float, default=20.0, help="seconds to look for the recorder")
    g.add_argument("--heartbeat", type=float, default=0,
                   help="send APP&WPING every N seconds while on the AP (the app's Wi-Fi heartbeat); 0 = off")
    g.add_argument("--status-interval", type=float, default=2.0,
                   help="ask WIFIS every N seconds while on the AP, 0 to never ask (default 2)")

    g = p.add_argument_group("WiFi")
    g.add_argument("--wifi", choices=["auto", "networkmanager", "netsh", "manual"], default="auto",
                   help="how to join the AP: NetworkManager (Linux), netsh (Windows), or by hand")
    g.add_argument("--iface", help="WiFi interface (default: the first one)")
    g.add_argument("--join-timeout", type=float, default=45.0, help="seconds to join after WIFIO (default 45)")
    g.add_argument("--ready-timeout", type=float, default=30.0, help="seconds to wait for WIFIS=1 (default 30)")

    g = p.add_argument_group("probing")
    g.add_argument("--check-timeout", type=float, default=600.0, help="seconds one check may take (default 600)")
    g.add_argument("--stream-port", type=int, default=8475,
                   help="the recorder's transfer socket, for the stream check (default 8475)")
    g.add_argument("--stream-trigger", default="U&{date}&{ts},U&WIFI",
                   help="stream: Bluetooth commands sent once connected, comma-separated; {date} and "
                        "{ts} are --stream-recording's; wait:OFF waits for MCU&OFF (end of a file), "
                        "sleep:N pauses, close closes the connections so far, reconnect opens another one, "
                        "cycle closes them and restarts the AP (WIFIC, WIFIO, rejoin, WIFIS=1) (default: start a Bluetooth transfer, then switch it to WiFi with "
                        "U&WIFI, as the official app does)")
    g.add_argument("--stream-recording",
                   help="stream: recording for {date}/{ts}, DATE/TIMESTAMP (default: the longest)")
    g.add_argument("--stream-gap", type=float, default=0.3,
                   help="stream: seconds between trigger commands (default 0.3; the app waits 0.35)")
    g.add_argument("--stream-wait", type=float, default=30.0,
                   help="stream: keep trying to connect for N seconds (default 30)")
    g.add_argument("--stream-idle", type=float, default=10.0,
                   help="stream: stop reading after N seconds without data (default 10)")
    g.add_argument("--stream-max", type=float, default=120.0,
                   help="stream: stop reading after N seconds in all (default 120)")
    g.add_argument("--tcp-ports", default="1-65535", help="ports tcp-scan sweeps (default 1-65535)")
    g.add_argument("--tcp-timeout", type=float, default=0.5, help="connect timeout per port (default 0.5)")
    g.add_argument("--tcp-concurrency", type=int, default=0, help="parallel connects (default 400; 200 on Windows)")
    g.add_argument("--udp-ports", default="53,67,69,123,137,161,500,1900,3702,5353,5683,6666,7777,8000,"
                   "8080,8266,8888,9000,9999,10000,12345,20000,30000,40000,50000,55555,60000",
                   help="ports udp-probe tries")
    g.add_argument("--listen-ports", default="80,443,1234,5000-5010,6666,7777,8000-8010,8080,8266,8888,"
                   "9000-9010,9999,10000,12345,20000,30000,40000,50000-50010,55555,60000",
                   help="ports the inbound monitor listens on (TCP and UDP)")
    g.add_argument("--hold", type=float, default=0, help="keep the AP up N seconds after the checks")
    g.add_argument("--pause", action="store_true", help="wait for Enter before tearing down "
                   "(time to poke around by hand)")
    return p


def _names(text: str) -> list[str]:
    return [n.strip() for n in text.split(",") if n.strip()]


def list_checks() -> None:
    items = sorted(checks.load().values(), key=lambda c: (c.priority, c.order))
    for c in items:
        where = "monitor" if c.kind == "monitor" else ",".join(c.phases)
        flag = "" if c.default else "  (off by default)"
        print(f"  {c.name:<16} {where:<18} {c.help}{flag}")


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    if args.list_checks:
        list_checks()
        return 0
    args.key = args.key or os.environ.get("POCKET_SESSION_KEY")
    if not args.address or not args.key:
        parser().error("the Bluetooth address and the session key are required")
    if not re.fullmatch(r"[0-9A-Fa-f]{2}([:-][0-9A-Fa-f]{2}){5}|[0-9A-Fa-f]{8}-[0-9A-Fa-f-]{27}", args.address):
        parser().error("the address should look like AA:BB:CC:DD:EE:FF")
    if not re.fullmatch(r"[\x21-\x25\x27-\x7e]{8,64}", args.key):
        parser().error("the session key should be 8-64 printable characters without '&'")
    args.out = args.out or f"pocket-probe-{dt.datetime.now():%Y%m%d-%H%M%S}.json"
    if args.tcp_concurrency <= 0:
        args.tcp_concurrency = 200 if sys.platform == "win32" else 400
    try:
        return asyncio.run(probe(args))
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    sys.exit(main())
