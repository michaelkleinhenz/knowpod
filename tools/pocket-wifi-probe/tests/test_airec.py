"""Tests for airec-probe against a fake recorder that answers the way the real one did in the
capture of 2026-10-15 (no Bluetooth needed)."""
import argparse
import asyncio
import random

from pocket_wifi_probe import airec


def slot(seed: int, pad: int = 10) -> bytes:
    """An 80-byte Opus packet like the recorder's: TOC 4b, one frame, `pad` zero bytes."""
    rnd = random.Random(seed)
    body = bytes(rnd.randrange(1, 256) for _ in range(airec.SLOT - 3 - pad))
    return bytes([0x4B, 0x41, pad]) + body + bytes(pad)


def recording(n: int, cut_at: int | None = None) -> bytes:
    """n packets; with cut_at, that packet is cut short by 16 bytes, as in two captured files."""
    data = b""
    for i in range(n):
        s = slot(i, pad=5 + i % 20)
        data += s[:-16] if i == cut_at else s
    return data


class FakeRecorder(airec.Airec):
    """Answers commands like firmware 1.3.3 did. File data goes out in 244-byte chunks."""

    def __init__(self, files: dict[str, bytes], *, need_prep: bool = False, live: bytes = b""):
        super().__init__()
        self.files = dict(files)
        self.need_prep, self.prepped = need_prep, False
        self.clock = "20261015230000"
        self.settings = bytes.fromhex("010101003c0000001e01050101")
        self.live_audio = live
        self.sent: list[bytes] = []

    def reply(self, cmd: int, data: bytes = b"") -> None:
        self.feed_reply(bytes([0xAA, 0x55, len(data) + 1, cmd]) + data)

    async def write(self, raw: bytes) -> None:
        assert raw[:2] == b"\x55\xaa" and raw[2] == len(raw) - 3
        self.sent.append(raw)
        cmd, data = raw[3], raw[4:]
        await asyncio.sleep(0)
        if cmd == 0x01:
            self.reply(0x01, b"206112260800013")
            self.reply(0x36, b"\x00")
        elif cmd == 0x20:
            self.reply(0x20, b"\x10")
        elif cmd == 0x12:
            self.reply(0x12, b"1.3.3")
        elif cmd == 0x29:
            self.reply(0x29, b"A5AB")
        elif cmd == 0x26:
            self.reply(0x26, self.settings)
        elif cmd == 0x0F:
            self.reply(0x0F, b"\x02")
        elif cmd == 0x0E:
            self.reply(0x0E, b"\x64")
        elif cmd == 0x0B:
            self.reply(0x0C, (29805).to_bytes(4, "big"))
            self.reply(0x0D, (29818).to_bytes(4, "big"))
        elif cmd == 0x30:
            self.reply(0x30, self.clock.encode())
        elif cmd == 0x02:
            self.clock = data.decode()
            self.reply(0x02)
        elif cmd == 0x05:
            for name, content in self.files.items():
                self.reply(0x05, name.encode() + len(content).to_bytes(4, "big"))
            self.reply(0x06)
        elif cmd == 0x69:
            self.prepped = True
        elif cmd == 0x07:
            name, offset = data[:14].decode(), int.from_bytes(data[14:18], "big")
            if name not in self.files or (self.need_prep and not self.prepped):
                return
            content = self.files[name]
            self.reply(0x07, name.encode() + len(content).to_bytes(4, "big"))
            for i in range(offset, len(content), 244):
                self.feed_data(content[i:i + 244])
                await asyncio.sleep(0)
            self.reply(0x09)
        elif cmd == 0x0A:
            name = data.decode()
            if name in self.files:
                del self.files[name]
                self.reply(0x0A)
            else:
                self.reply(0xFC)
        elif cmd == 0x22:
            self.settings = self.settings[:3] + data + self.settings[5:]
            self.reply(0x22)

    async def record(self, name: str) -> None:
        """What a recording made on the recorder sends, unasked."""
        await asyncio.sleep(0.05)
        self.reply(0x03, name.encode())
        for i in range(0, len(self.live_audio), 240):
            self.reply(0x3B, b"\x00\x00")
            self.feed_live(self.live_audio[i:i + 240])
            await asyncio.sleep(0)
        self.reply(0x10)
        self.files[name] = self.live_audio
        self.reply(0x04, name.encode() + len(self.live_audio).to_bytes(4, "big"))


def args(**kw) -> argparse.Namespace:
    base = dict(checks=None, skip=None, file=None, explore=False, allow_write=False, delete=None,
                no_34=False, live=None)
    base.update(kw)
    return argparse.Namespace(**base)


def run_checks(rec, tmp_path, **kw):
    a = args(**kw)
    ctx = airec.Ctx(rec, a, tmp_path, lambda kind, text: None)

    async def go():
        return await airec.run(rec, airec.select(a), ctx)

    return {r.name: r for r in asyncio.run(go())}


def test_frames_split_and_joined():
    p = airec.FrameParser()
    whole = bytes.fromhex("aa55020e64") + bytes.fromhex("aa550106")
    assert p.feed(whole[:3]) == []
    assert p.feed(whole[3:]) == [(0x0E, b"\x64"), (0x06, b"")]
    assert p.feed(b"\x00\x01aa") == [] and p.skipped == 4  # stray bytes are counted
    assert p.feed(bytes.fromhex("aa550102")) == [(0x02, b"")]
    assert airec.build(0x07, b"x" * 18)[:4] == bytes.fromhex("55aa1307")


def test_opus_slots_and_realignment():
    assert airec.slot_ok(slot(1))
    assert not airec.slot_ok(bytes([0x4F]) + slot(1)[1:])  # stereo bit set
    packets, resyncs, left = airec.split_slots(recording(30, cut_at=12) + slot(99)[:16])
    assert len(packets) == 29  # the cut packet is dropped, the rest found again
    assert resyncs == [12 * airec.SLOT]
    assert left == 16


def test_read_only_run_matches_the_capture(tmp_path):
    rec = FakeRecorder({"20261015183232": recording(50), "20261015230312": recording(20, cut_at=7)})
    res = run_checks(rec, tmp_path)
    assert {n: r.status for n, r in res.items()} == {
        "no-auth": "PASS", "chip-type": "PASS", "firmware": "PASS", "model": "PASS",
        "settings": "PASS", "state": "PASS", "battery": "PASS", "storage": "PASS", "clock": "PASS",
        "list": "PASS", "download": "PASS", "audio": res["audio"].status, "resume": "PASS"}
    assert res["audio"].status in ("PASS", "INFO")  # INFO without libopus
    assert "20261015230312" in res["download"].detail  # the smallest file
    assert "69/66 not needed" in res["download"].detail
    assert res["audio"].data["resyncs_at"] == [7 * airec.SLOT]
    assert (tmp_path / "20261015230312.bin").read_bytes() == rec.files["20261015230312"]
    # nothing that changes the recorder was sent
    assert not {raw[3] for raw in rec.sent} & {0x02, 0x0A, 0x22, 0x34}


def test_download_falls_back_to_the_apps_preparation(tmp_path):
    rec = FakeRecorder({"20261015183232": recording(10)}, need_prep=True)
    res = run_checks(rec, tmp_path, checks="list,download")
    assert res["download"].status == "PASS"
    assert "needed 69/66" in res["download"].detail


def test_write_checks(tmp_path):
    rec = FakeRecorder({"20261015183232": recording(10), "20261015230312": recording(5)})
    res = run_checks(rec, tmp_path, checks="set-clock,delete-missing,delete", allow_write=True,
                     delete="20261015230312")
    assert [r.status for r in res.values()] == ["PASS", "PASS", "PASS"]
    assert list(rec.files) == ["20261015183232"]


def test_write_checks_need_the_flag():
    try:
        airec.select(args(checks="set-clock"))
    except SystemExit as e:
        assert "--allow-write" in str(e)
    else:
        raise AssertionError("set-clock ran without --allow-write")


def test_explore_writes_back_the_current_setting(tmp_path):
    rec = FakeRecorder({})
    res = run_checks(rec, tmp_path, checks="explore", explore=True)
    assert res["explore"].status == "INFO"
    assert "settings unchanged" in res["explore"].detail
    assert bytes.fromhex("55aa0322003c") in rec.sent


def test_live_recording(tmp_path):
    rec = FakeRecorder({}, live=recording(50))
    a = args(checks="live", live=5.0)
    ctx = airec.Ctx(rec, a, tmp_path, lambda kind, text: None)

    async def go():
        task = asyncio.create_task(airec.run(rec, airec.select(a), ctx))
        await rec.record("20261015170405")
        return await task

    (result,) = asyncio.run(go())
    assert result.status == "PASS", result.detail
    assert "saved 20261015170405 4000 B" in result.detail
