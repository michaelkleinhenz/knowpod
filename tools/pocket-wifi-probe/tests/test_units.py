import struct

import pytest

from pocket_wifi_probe import checks
from pocket_wifi_probe.checks.udp import dns_parse, dns_query
from pocket_wifi_probe.hostwifi import parse_netsh_interfaces, split_terse, windows_profile
from pocket_wifi_probe.pocket import Message, split_messages
from pocket_wifi_probe.util import parse_ports


def test_split_messages():
    assert split_messages("MCU&WIFIO\0") == ["MCU&WIFIO"]
    assert split_messages("MCU&WIFIOMCU&OFF") == ["MCU&WIFIO", "MCU&OFF"]


def test_message_value():
    m = Message(0, "MCU&WIFI&PKT01_GREY_1&abcdefgh", "x")
    assert m.value("WIFI") == "PKT01_GREY_1&abcdefgh"
    assert m.value("WIFIS") is None
    assert Message(0, "MCU&WIFIO", "x").value("WIFIO") == ""


def test_parse_ports():
    assert parse_ports("80, 1-3,2") == [80, 1, 2, 3]
    with pytest.raises(ValueError):
        parse_ports("70000")


def test_split_terse():
    assert split_terse(r"Home\:Net:1234:802-11-wireless:wlan0") == ["Home:Net", "1234", "802-11-wireless", "wlan0"]


def test_netsh_english_and_german():
    en = """
There is 1 interface on the system:

    Name                   : Wi-Fi
    Description            : Intel(R) Wi-Fi 6
    State                  : connected
    SSID                   : Home
    Profile                : Home
"""
    de = """
    Name                   : WLAN
    Beschreibung           : Intel(R) Wi-Fi 6
    Status                 : Verbunden
    SSID                   : Zuhause
    Profil                 : Zuhause
"""
    assert parse_netsh_interfaces(en) == [{"name": "Wi-Fi", "state": "connected", "ssid": "Home", "profile": "Home"}]
    assert parse_netsh_interfaces(de)[0]["profile"] == "Zuhause"
    assert parse_netsh_interfaces("    Name : WLAN\n    Status : Getrennt\n")[0].get("profile") is None


def test_windows_profile_escapes():
    xml = windows_profile("A&B", "p<w>")
    assert "<name>A&amp;B</name>" in xml and "<keyMaterial>p&lt;w&gt;</keyMaterial>" in xml


def test_dns_roundtrip():
    q = dns_query("pocket.local", ident=7)
    # A fake answer: the query plus one A record pointing back at the question name.
    header = struct.pack(">HHHHHH", 7, 0x8180, 1, 1, 0, 0)
    answer = b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 60, 4) + bytes([192, 168, 200, 1])
    parsed = dns_parse(header + q[12:] + answer)
    assert parsed["rcode"] == 0
    assert parsed["answers"] == [{"type": 1, "ttl": 60, "data": "192.168.200.1"}]


def test_registry():
    reg = checks.load()
    assert {"tcp-scan", "tcp-probe", "udp-probe", "dns", "inbound", "capture", "gatt"} <= set(reg)
    names = [c.name for c in checks.select(None, ["capture"])]
    assert "capture" not in names and names.index("tcp-scan") < names.index("tcp-probe")
    with pytest.raises(ValueError):
        checks.select(["nope"], [])


def test_duplicate_notifications_dropped():
    from pocket_wifi_probe.pocket import COMMAND, PocketLink
    link = PocketLink("AA:BB:CC:DD:EE:FF")
    handle = link._handler(COMMAND)
    for _ in range(4):
        handle(None, bytearray(b"MCU&WIFIS&1"))
    handle(None, bytearray(b"MCU&WIFIS&2"))
    assert [m.text for m in link.messages] == ["MCU&WIFIS&1", "MCU&WIFIS&2"]
    assert link.duplicates == 3


def test_stream_runs_first():
    names = [c.name for c in checks.select(None, [])]
    assert names.index("stream") < names.index("tcp-scan")


def test_networkmanager_join_does_not_reconnect_while_activating(monkeypatch):
    """nmcli's --wait can run out while the link is still coming up (it did on a real run);
    asking again would drop and redo the association, so join must just keep waiting."""
    import asyncio
    import time

    from pocket_wifi_probe import hostwifi
    from pocket_wifi_probe.util import Result

    calls = []
    joined_at = time.monotonic() + 1.5

    async def fake_run(*args, timeout=30.0):
        calls.append(args)
        if "--wait" in args and "up" in args:
            return Result(4, "", "Error: Timeout expired (15 seconds)")
        if "GENERAL.STATE" in args:  # active only once "up" was asked for
            up = any("up" in c for c in calls)
            return Result(0, "GENERAL.STATE:activating\n", "") if up else Result(10, "", "no such connection")
        return Result(0, "", "")

    monkeypatch.setattr(hostwifi, "run", fake_run)
    nm = hostwifi.NetworkManager("192.168.200.1", "wlan0", lambda *a: None)
    monkeypatch.setattr(nm, "on_ap", lambda: "192.168.200.2" if time.monotonic() > joined_at else None)
    assert asyncio.run(nm.join("PKT01", time.monotonic() + 10))
    assert sum(1 for c in calls if "up" in c) == 1
