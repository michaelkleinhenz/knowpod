"""The whole run against a fake firmware 1.8 recorder whose "access point" is localhost."""
import asyncio
import json
import socket
import struct

from pocket_wifi_probe import session
from pocket_wifi_probe.cli import parser
from pocket_wifi_probe.hostwifi import HostWifi
from pocket_wifi_probe.pocket import COMMAND, PocketLink

KEY = "abcdefgh12345678"


class FakePocket(PocketLink):
    sent: list[str] = []

    async def __aenter__(self):
        self.status = 0
        return self

    async def close(self):
        pass

    @property
    def connected(self):
        return True

    def _say(self, text, delay=0.01):
        asyncio.get_running_loop().call_later(delay, self._handler(COMMAND), None, bytearray(text.encode()))

    async def send(self, command):
        FakePocket.sent.append(command)
        since = self.mark()
        self.on_event("ble>", f"APP&{command}")
        answers = {
            f"SK&{KEY}": ["MCU&SK&OK"],
            "FW": ["MCU&FW&1.8.0"],
            "STE": ["MCU&STE&0"],
            "LIST_DIRS": ["MCU&DIRS&2026-09-03", "MCU&DIRS_SUM&001"],
            "LIST&2026-09-03": ["MCU&F&2026-09-03&20260903145856&10MCU&LIST&001"],
            "WIFI": [f"MCU&WIFI&PKT01_GREY_TEST&{KEY[:8]}"],
            "U&2026-09-03&20260903145856": ["MCU&U&6653128"],
            "WIFIC": ["MCU&WIFIC"],
        }
        for text in answers.get(command, []):
            self._say(text)
        if command == "WIFIO":
            self._say("MCU&WIFIOMCU&OFF")
            self._say("MCU&WIFIS&3", 0.05)
            self._say("MCU&WIFIS&1", 0.2)
        if command == "WIFIS":
            self._say("MCU&WIFIS&1")
        return since


class FakeWifi(HostWifi):
    calls: list[str] = []

    async def setup(self):
        FakeWifi.calls.append("setup")

    async def prepare(self, ssid, password):
        FakeWifi.calls.append(f"prepare {ssid}")

    async def join(self, ssid, deadline):
        FakeWifi.calls.append("join")
        return True

    async def restore(self):
        FakeWifi.calls.append("restore")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def test_full_run(tmp_path, monkeypatch):
    monkeypatch.setattr(session, "PocketLink", FakePocket)
    monkeypatch.setattr(session, "backend", lambda kind, host, iface, log: FakeWifi(host, iface, log))
    FakePocket.sent, FakeWifi.calls = [], []
    talker, closed, listen = free_port(), free_port(), free_port()
    out = tmp_path / "r.json"
    args = parser().parse_args([
        "AA:BB:CC:DD:EE:FF", KEY, "--host", "127.0.0.1", "--out", str(out), "-q",
        "--checks", "ble-state,tcp-scan,tcp-probe,udp-probe,inbound",
        "--tcp-ports", f"{talker},{closed}", "--udp-ports", str(closed), "--listen-ports", str(listen),
        "--begin-wait", "0.2", "--status-interval", "0.2",
    ])
    args.tcp_concurrency = 4

    async def main():
        async def hello(reader, writer):
            writer.write(b"HELLO")
            await writer.drain()
            writer.close()
        server = await asyncio.start_server(hello, "127.0.0.1", talker)
        async with server:
            return await session.probe(args)

    assert asyncio.run(main()) == 0
    report = json.loads(out.read_text())
    text = out.read_text()

    # The firmware 1.8 order: stage before WIFIO, begin after, clean up last.
    order = [c for c in FakePocket.sent if c in
             ("U&WIFI", "WIFI", "U&2026-09-03&20260903145856", "WIFIO", "WIFIC")]
    assert order == ["U&WIFI", "WIFI", "U&2026-09-03&20260903145856", "WIFIO", "U&WIFI", "WIFIC"]
    assert FakeWifi.calls == ["setup", "prepare PKT01_GREY_TEST", "join", "restore"]

    assert KEY not in text and KEY[:8] not in text  # secrets redacted
    assert report["meta"]["staged"] == "2026-09-03/20260903145856"
    assert report["meta"]["wifi_status_history"][-1][1] == 1
    ready = report["results"]["ready"]
    assert ready["tcp-scan"]["result"]["open"] == [talker]
    assert ready["tcp-probe"]["result"][f"{talker}/listen"]["received"]["ascii"] == "HELLO"
    assert ready["udp-probe"]["result"]["summary"] == {"closed": [closed]}
    assert "begin" in report["results"]
    assert report["results"]["monitors"]["inbound"]["ok"]


def test_refuses_other_firmware(tmp_path, monkeypatch):
    class OldPocket(FakePocket):
        async def send(self, command):
            if command == "FW":
                since = self.mark()
                self._say("MCU&FW&1.3.3")
                return since
            return await super().send(command)

    monkeypatch.setattr(session, "PocketLink", OldPocket)
    monkeypatch.setattr(session, "backend", lambda kind, host, iface, log: FakeWifi(host, iface, log))
    FakePocket.sent, FakeWifi.calls = [], []
    out = tmp_path / "r.json"
    args = parser().parse_args(["AA:BB:CC:DD:EE:FF", KEY, "--out", str(out), "-q", "--checks", "ble-state"])
    assert asyncio.run(session.probe(args)) == 1
    assert "WIFIO" not in FakePocket.sent and FakeWifi.calls == []
    assert "not 1.8" in out.read_text()


def test_stream_connects_then_triggers(tmp_path, monkeypatch):
    """stream connects before sending U&WIFI, and saves what the recorder sends after it."""
    started = asyncio.Event()

    class StreamingPocket(FakePocket):
        async def send(self, command):
            if command == "WIFI&SWITCH":
                started.set()
            return await super().send(command)

    monkeypatch.setattr(session, "PocketLink", StreamingPocket)
    monkeypatch.setattr(session, "backend", lambda kind, host, iface, log: FakeWifi(host, iface, log))
    FakePocket.sent, FakeWifi.calls = [], []
    port = free_port()
    out = tmp_path / "r.json"
    frame = struct.pack("<HI", 0xA55A, 6653128) + b"\xff\xf3\x48\xc4" + b"\0" * 20
    args = parser().parse_args([
        "AA:BB:CC:DD:EE:FF", KEY, "--host", "127.0.0.1", "--out", str(out), "-q",
        "--checks", "stream", "--no-begin", "--stream-port", str(port), "--stream-idle", "0.5",
        "--status-interval", "0", "--stream-gap", "0.1", "--heartbeat", "0.2",
    ])

    async def main():
        async def serve(reader, writer):
            await started.wait()  # silent until told over Bluetooth
            writer.write(frame)
            await writer.drain()
            writer.close()
        server = await asyncio.start_server(serve, "127.0.0.1", port)
        async with server:
            return await session.probe(args)

    assert asyncio.run(main()) == 0
    res = json.loads(out.read_text())["results"]["ready"]["stream"]["result"]
    assert res["before_trigger"] == "idle" and res["bytes_before_trigger"] == 0
    assert res["after_trigger"] == "closed"
    assert res["data"]["length"] == len(frame) and res["data"]["mp3_sync_offsets"] == [6]
    assert {"offset": 2, "type": "u32le", "value": 6653128, "equals": "staged file size"} in res["data"]["length_fields"]
    assert (tmp_path / f"r.{port}.bin").read_bytes() == frame
    # Connected first, then: start a Bluetooth transfer of the (longest) recording, switch it.
    after = FakePocket.sent[FakePocket.sent.index("WIFIO") + 1:]
    assert [c for c in after if c not in ("WIFIS", "WPING")][:2] == ["U&2026-09-03&20260903145856", "WIFI&SWITCH"]
    assert "WPING" in FakePocket.sent and res["recording"] == "2026-09-03/20260903145856"


def test_stream_waits_for_the_port(tmp_path, monkeypatch):
    """The socket may open a few seconds after WIFIS=1; refused connects are retried."""
    monkeypatch.setattr(session, "PocketLink", FakePocket)
    monkeypatch.setattr(session, "backend", lambda kind, host, iface, log: FakeWifi(host, iface, log))
    FakePocket.sent, FakeWifi.calls = [], []
    port = free_port()
    out = tmp_path / "r.json"
    args = parser().parse_args([
        "AA:BB:CC:DD:EE:FF", KEY, "--host", "127.0.0.1", "--out", str(out), "-q",
        "--checks", "stream", "--no-begin", "--stream-port", str(port), "--stream-idle", "0.3",
        "--stream-wait", "10", "--status-interval", "0",
    ])

    async def main():
        async def serve(reader, writer):
            writer.close()

        async def open_later():
            await asyncio.sleep(4)  # well after the stream check starts trying
            return await asyncio.start_server(serve, "127.0.0.1", port)

        opener = asyncio.create_task(open_later())
        code = await session.probe(args)
        (await opener).close()
        return code

    assert asyncio.run(main()) == 0
    res = json.loads(out.read_text())["results"]["ready"]["stream"]["result"]
    assert res["connected"] and res["attempts"] > 1 and res["waited"] >= 1
