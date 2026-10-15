"""airec-probe: tests what we believe about the AIREC recorder's Bluetooth protocol, against a
recorder you own.

What we believe comes from one capture of the vendor app (com.record.airec.google 2.1.5, recorder
firmware 1.3.3, 2026-10-15). Everything runs over Bluetooth LE, service 0011200a-…:

    0011202a  write without response   commands:  55 AA <len> <cmd> <data>   (len = 1 + data)
    0011203a  notify                   replies:   AA 55 <len> <cmd> <data>   (and events)
    0011204a  notify                   file data, after a download command
    0011201a  notify                   live audio while the recorder records

No Bluetooth pairing, no unlock: the app's "pairing" binds the serial number to the account on
the vendor's server. Recordings are named after their start time (YYYYMMDDHHMMSS) and stored as
back-to-back 80-byte Opus packets (20 ms, 16 kHz mono, padded to 80 bytes), 4000 bytes a second.

Each check states one assumption and reports PASS (it holds), FAIL (it doesn't), INFO (nothing to
compare against; the value is shown) or SKIP. By default the probe only reads: the recorder is
left as it was. Changing anything needs a flag:

    --explore       sends the commands whose purpose we don't know yet (36, 69/66, 22 with the
                    current value, an unknown command) and reports what comes back
    --allow-write   sets the recorder's clock (the app does this on every connect) and tries to
                    delete a file that doesn't exist
    --delete NAME   (with --allow-write) deletes that recording from the recorder: it's gone
    --live SECONDS  waits that long for you to record on the recorder, and checks the events
                    and the live audio

Everything goes to airec-<time>/: report.json, frames.log (every frame sent and received, with
time) and the downloaded files (.bin as received, .wav when libopus is installed).
"""
from __future__ import annotations

import argparse
import asyncio
import ctypes
import ctypes.util
import datetime as dt
import json
import re
import time
import traceback
import wave
from collections import Counter
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Awaitable, Callable

SERVICE = "0011200a-2233-4455-6677-8899dfdedddc"
LIVE = "0011201a-2233-4455-6677-8899dfdedddc"
COMMAND = "0011202a-2233-4455-6677-8899dfdedddc"
REPLY = "0011203a-2233-4455-6677-8899dfdedddc"
FILE_DATA = "0011204a-2233-4455-6677-8899dfdedddc"
NAME_RE = r"^AIREC"

SLOT = 80  # bytes per Opus packet in a recording
FRAME_MS = 20
BYTES_PER_SECOND = SLOT * 1000 // FRAME_MS
SAMPLE_RATE = 16000
MISSING_NAME = "20000101000000"  # a recording name that can't exist

NAMES = {
    0x01: "serial", 0x02: "set clock", 0x03: "recording started", 0x04: "file saved",
    0x05: "file entry", 0x06: "list end", 0x07: "download", 0x09: "download done",
    0x0A: "delete", 0x0B: "storage", 0x0C: "storage free", 0x0D: "storage total",
    0x0E: "battery", 0x0F: "state?", 0x10: "recording stopped", 0x12: "firmware",
    0x20: "chip type", 0x22: "setting 22?", 0x26: "settings", 0x29: "model", 0x30: "clock",
    0x34: "before delete?", 0x36: "36?", 0x3B: "recording status", 0x66: "66?", 0x69: "69?",
    0xFC: "error?",
}


# --- Frames ---------------------------------------------------------------------------------

@dataclass
class Frame:
    t: float  # monotonic seconds
    cmd: int
    data: bytes

    def text(self) -> str:
        return self.data.decode("ascii", "replace")

    def describe(self) -> str:
        name = NAMES.get(self.cmd, "?")
        # Text only from 3 bytes on: a single 64 (100 %) would otherwise show as "d".
        shown = self.text() if len(self.data) >= 3 and all(32 <= c < 127 for c in self.data) else self.data.hex()
        return f"{self.cmd:02x} ({name}){' ' + shown if self.data else ''}"


def build(cmd: int, data: bytes = b"") -> bytes:
    """A command frame: 55 AA <len> <cmd> <data>, len counting cmd and data."""
    return bytes([0x55, 0xAA, len(data) + 1, cmd]) + data


class FrameParser:
    """Splits the reply stream (AA 55 <len> <cmd> <data>) into frames. A notification may hold
    several frames or part of one; bytes that don't start a frame are counted and dropped."""

    def __init__(self):
        self.buf = bytearray()
        self.skipped = 0

    def feed(self, chunk: bytes) -> list[tuple[int, bytes]]:
        self.buf += chunk
        out = []
        while True:
            i = self.buf.find(b"\xaa\x55")
            if i < 0:
                keep = 1 if self.buf[-1:] == b"\xaa" else 0
                self.skipped += len(self.buf) - keep
                del self.buf[:len(self.buf) - keep]
                return out
            if i:
                self.skipped += i
                del self.buf[:i]
            if len(self.buf) < 3:
                return out
            n = self.buf[2]
            if n == 0:  # no command byte: not a frame
                self.skipped += 2
                del self.buf[:2]
                continue
            if len(self.buf) < 3 + n:
                return out
            out.append((self.buf[3], bytes(self.buf[4:3 + n])))
            del self.buf[:3 + n]


def parse_entry(data: bytes) -> tuple[str, int]:
    """A file list entry or download header: 14-character name, then a 4-byte big-endian size."""
    return data[:14].decode("ascii", "replace"), int.from_bytes(data[14:18], "big")


def parse_time(text: str) -> dt.datetime | None:
    try:
        return dt.datetime.strptime(text, "%Y%m%d%H%M%S")
    except ValueError:
        return None


# --- The protocol, over any transport ------------------------------------------------------

class Airec:
    """The command protocol. Subclasses provide `write`, and feed what the recorder notifies
    into feed_reply / feed_data / feed_live."""

    def __init__(self, on_frame: Callable[[str, Frame], None] | None = None):
        self.frames: list[Frame] = []  # everything the recorder sent on the reply characteristic
        self.parser = FrameParser()
        self.file_data = bytearray()
        self.data_chunks: Counter = Counter()  # notification sizes on the file data characteristic
        self.first_data = self.last_data = 0.0
        self.live = bytearray()
        self.disconnected = False
        self.on_frame = on_frame or (lambda direction, frame: None)
        self._changed = asyncio.Event()

    async def write(self, raw: bytes) -> None:
        raise NotImplementedError

    def feed_reply(self, chunk: bytes) -> None:
        for cmd, data in self.parser.feed(chunk):
            f = Frame(time.monotonic(), cmd, data)
            self.frames.append(f)
            self.on_frame("<", f)
        self._changed.set()

    def feed_data(self, chunk: bytes) -> None:
        now = time.monotonic()
        if not self.file_data:
            self.first_data = now
        self.file_data += chunk
        self.last_data = now
        self.data_chunks[len(chunk)] += 1
        self._changed.set()

    def feed_live(self, chunk: bytes) -> None:
        self.live += chunk
        self._changed.set()

    def mark(self) -> int:
        return len(self.frames)

    async def send(self, cmd: int, data: bytes = b"") -> None:
        if self.disconnected:
            raise ConnectionError("the recorder disconnected")
        self.on_frame(">", Frame(time.monotonic(), cmd, data))
        await self.write(build(cmd, data))

    async def wait_for(self, cmds: set[int], since: int, timeout: float) -> Frame | None:
        """The first frame with one of `cmds` received after frame number `since`."""
        deadline = time.monotonic() + timeout
        while True:
            for f in self.frames[since:]:
                if f.cmd in cmds:
                    return f
            remaining = deadline - time.monotonic()
            if remaining <= 0 or self.disconnected:
                return None
            self._changed.clear()
            try:
                await asyncio.wait_for(self._changed.wait(), remaining)
            except asyncio.TimeoutError:
                pass

    async def request(self, cmd: int, data: bytes = b"", reply: int | set[int] | None = None,
                      timeout: float = 3.0) -> Frame | None:
        since = self.mark()
        await self.send(cmd, data)
        want = {cmd} if reply is None else {reply} if isinstance(reply, int) else reply
        return await self.wait_for(want, since, timeout)

    async def listen(self, seconds: float, since: int | None = None) -> list[Frame]:
        """Everything received in the next `seconds` (or since frame number `since`)."""
        start = self.mark() if since is None else since
        await asyncio.sleep(seconds)
        return self.frames[start:]

    # --- higher-level operations

    async def list_files(self, timeout: float = 10.0) -> tuple[list[tuple[str, int]], bool]:
        """(entries, whether the list-end frame came)."""
        since = self.mark()
        await self.send(0x05)
        end = await self.wait_for({0x06}, since, timeout)
        await asyncio.sleep(0.1)
        entries = [parse_entry(f.data) for f in self.frames[since:] if f.cmd == 0x05]
        return entries, end is not None

    async def download(self, name: str, offset: int = 0, prep: bool = True,
                       reply_timeout: float = 5.0, stall: float = 5.0) -> dict:
        """Asks for `name` from `offset` and collects the file data until the done frame, or until
        no data came for `stall` seconds."""
        if prep:  # the app sends these before every download; what they do is unknown
            await self.send(0x69, b"\x00")
            await asyncio.sleep(0.15)
            await self.send(0x66, b"\x00")
            await asyncio.sleep(0.15)
        self.file_data.clear()
        self.data_chunks.clear()
        since = self.mark()
        t0 = time.monotonic()
        head = await self.request(0x07, name.encode() + offset.to_bytes(4, "big"), reply={0x07, 0xFC},
                                  timeout=reply_timeout)
        result: dict = {"name": name, "offset": offset, "prep": prep, "reply": head is not None}
        if head is None:
            return result
        if head.cmd == 0xFC:
            result["error"] = head.data.hex()
            return result
        result["echo"], result["size"] = parse_entry(head.data)
        after = self.frames.index(head, since) + 1
        done, seen, seen_t = None, -1, time.monotonic()
        while done is None:
            done = await self.wait_for({0x09, 0xFC}, after, 1.0)
            now = time.monotonic()
            if len(self.file_data) != seen:
                seen, seen_t = len(self.file_data), now
            elif now - seen_t > stall or self.disconnected:
                break
        await asyncio.sleep(0.3)  # data on the other characteristic may still be on its way
        data = bytes(self.file_data)
        busy = max(self.last_data - self.first_data, 1e-3) if data else 0
        result.update({
            "done": done.describe() if done else None,
            "received": len(data),
            "seconds": round(time.monotonic() - t0, 2),
            "bytes_per_second": round(len(data) / busy) if data else 0,
            "chunk_sizes": dict(self.data_chunks.most_common(4)),
            "data": data,
        })
        return result


class BleAirec(Airec):
    """The recorder over Bluetooth LE (bleak). Use as `async with BleAirec(...) as rec`."""

    def __init__(self, address: str | None, name_re: str = NAME_RE, scan_timeout: float = 20.0,
                 on_frame=None, on_event: Callable[[str, str], None] | None = None):
        super().__init__(on_frame)
        self.address, self.name_re, self.scan_timeout = address, name_re, scan_timeout
        self.on_event = on_event or (lambda kind, text: None)
        self.client = None
        self.device_name = self.device_address = None
        self.mtu = None
        self.leaving = False  # we're disconnecting, so a disconnect isn't the recorder's doing

    async def __aenter__(self) -> "BleAirec":
        from bleak import BleakClient, BleakScanner

        if self.address:
            device = await BleakScanner.find_device_by_address(self.address, timeout=self.scan_timeout)
        else:
            pattern = re.compile(self.name_re)
            device = await BleakScanner.find_device_by_filter(
                lambda d, ad: bool(pattern.search(d.name or ad.local_name or "")), timeout=self.scan_timeout)
        if device is None:
            raise ConnectionError(f"no recorder found ({self.address or 'name ' + self.name_re}); "
                                  "is it on, nearby, and not connected to the phone?")
        self.device_name, self.device_address = device.name, device.address
        self.on_event("ble", f"found {device.name} ({device.address}); connecting")
        self.client = BleakClient(device, disconnected_callback=self._lost)
        await self.client.connect()
        self.mtu = getattr(self.client, "mtu_size", None)
        for uuid, feed in ((REPLY, self.feed_reply), (FILE_DATA, self.feed_data), (LIVE, self.feed_live)):
            await self.client.start_notify(uuid, lambda _c, d, feed=feed: feed(bytes(d)))
        self.on_event("ble", f"connected, MTU {self.mtu}, subscribed to replies, file data and live audio")
        return self

    def _lost(self, _client) -> None:
        self.disconnected = True
        self.on_event("ble", "disconnected" if self.leaving else "the recorder disconnected")
        self._changed.set()

    async def __aexit__(self, *exc) -> None:
        self.leaving = True
        if self.client and self.client.is_connected:
            try:
                await self.client.disconnect()
            except Exception:  # noqa: BLE001 - leaving anyway
                pass

    async def write(self, raw: bytes) -> None:
        await self.client.write_gatt_char(COMMAND, raw, response=False)


# --- Audio ----------------------------------------------------------------------------------

TWENTY_MS_CONFIGS = {1, 5, 9, 13, 15, 19, 23, 27, 31}  # Opus TOC configs with 20 ms frames


def slot_ok(b: bytes) -> bool:
    """Whether 80 bytes read as one mono 20 ms Opus packet: code 0 (one frame), or code 3 with
    one frame and zero padding filling the slot."""
    if len(b) < SLOT:
        return False
    toc = b[0]
    if toc >> 3 not in TWENTY_MS_CONFIGS or toc & 0x04:  # 20 ms, mono
        return False
    code = toc & 0x03
    if code == 0:
        return True
    if code != 3 or b[1] & 0x3F != 1:
        return False
    if b[1] & 0x40:  # padding: the next byte says how many zero bytes end the packet
        pad = b[2]
        return pad < SLOT - 3 and not any(b[SLOT - pad:SLOT])
    return True


def split_slots(data: bytes) -> tuple[list[bytes], list[int], int]:
    """(packets, offsets where a slot didn't check out, bytes left at the end).

    Where a slot doesn't read as a packet, the next packet start is found (a slot that checks out
    and is followed by another, or by the end), as the app does ("KA80", with re-alignment)."""
    packets: list[bytes] = []
    resyncs: list[int] = []
    pos = 0
    while pos + SLOT <= len(data):
        if slot_ok(data[pos:pos + SLOT]):
            packets.append(data[pos:pos + SLOT])
            pos += SLOT
            continue
        resyncs.append(pos)
        nxt = next((p for p in range(pos + 1, len(data) - SLOT + 1)
                    if slot_ok(data[p:p + SLOT])
                    and (p + 2 * SLOT > len(data) or slot_ok(data[p + SLOT:p + 2 * SLOT]))), None)
        if nxt is None:
            break
        pos = nxt
    return packets, resyncs, len(data) - pos


class OpusDecoder:
    """libopus through ctypes, mono 16 kHz. Raises OSError when libopus isn't installed."""

    def __init__(self, rate: int = SAMPLE_RATE):
        path = ctypes.util.find_library("opus")
        if not path:
            raise OSError("libopus isn't installed (Debian/Ubuntu: apt install libopus0)")
        lib = ctypes.CDLL(path)
        lib.opus_decoder_create.restype = ctypes.c_void_p
        lib.opus_decoder_create.argtypes = [ctypes.c_int32, ctypes.c_int, ctypes.POINTER(ctypes.c_int)]
        lib.opus_decode.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int32,
                                    ctypes.POINTER(ctypes.c_int16), ctypes.c_int, ctypes.c_int]
        lib.opus_decoder_destroy.argtypes = [ctypes.c_void_p]
        err = ctypes.c_int()
        self.lib, self.rate = lib, rate
        self.dec = lib.opus_decoder_create(rate, 1, ctypes.byref(err))
        if err.value:
            raise OSError(f"opus_decoder_create failed ({err.value})")
        self.pcm = (ctypes.c_int16 * 5760)()

    def decode(self, packet: bytes) -> bytes | None:
        n = self.lib.opus_decode(self.dec, packet, len(packet), self.pcm, 5760, 0)
        return bytes(memoryview(self.pcm).cast("B")[:n * 2]) if n > 0 else None

    def close(self) -> None:
        self.lib.opus_decoder_destroy(self.dec)


def audio_report(data: bytes, wav_path: Path | None) -> dict:
    """Checks a recording's bytes as 80-byte Opus packets; writes a WAV when libopus is there."""
    packets, resyncs, left = split_slots(data)
    out = {
        "packets": len(packets),
        "resyncs_at": resyncs,
        "bytes_left": left,
        "seconds_by_size": round(len(data) / BYTES_PER_SECOND, 2),
        "seconds_by_packets": round(len(packets) * FRAME_MS / 1000, 2),
        "tocs": {f"{k:02x}": v for k, v in Counter(p[0] for p in packets).most_common(4)},
    }
    try:
        dec = OpusDecoder()
    except OSError as e:
        out["decode"] = str(e)
        return out
    ok = failed = 0
    pcm = bytearray()
    for p in packets:
        samples = dec.decode(p)
        if samples is None:
            failed += 1
        else:
            ok += 1
            pcm += samples
    dec.close()
    out.update({"decoded": ok, "decode_failed": failed,
                "samples_per_packet": len(pcm) // 2 // ok if ok else 0})
    if wav_path and pcm:
        with wave.open(str(wav_path), "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(SAMPLE_RATE)
            w.writeframes(bytes(pcm))
        out["wav"] = wav_path.name
    return out


# --- Checks ---------------------------------------------------------------------------------

PASS, FAIL, INFO, SKIP, ERROR = "PASS", "FAIL", "INFO", "SKIP", "ERROR"


@dataclass
class Result:
    name: str
    status: str
    claim: str
    detail: str
    data: dict = field(default_factory=dict)


@dataclass
class Ctx:
    rec: Airec
    args: argparse.Namespace
    out: Path
    log: Callable[[str, str], None]
    shared: dict = field(default_factory=dict)


CheckFn = Callable[[Ctx], Awaitable[tuple[str, str, dict] | tuple[str, str]]]


@dataclass
class Check:
    name: str
    group: str  # read, explore, write, live
    claim: str
    fn: CheckFn


CHECKS: list[Check] = []


def check(name: str, group: str, claim: str):
    def register(fn: CheckFn) -> CheckFn:
        CHECKS.append(Check(name, group, claim, fn))
        return fn
    return register


async def ask(rec: Airec, cmd: int, data: bytes = b"", reply: int | set[int] | None = None,
              timeout: float = 3.0) -> Frame | None:
    return await rec.request(cmd, data, reply, timeout)


@check("no-auth", "read", "Commands work right after connecting: no Bluetooth pairing, no unlock. "
       "01 answers the serial number (ASCII digits), and the recorder also sends 36 00.")
async def check_no_auth(ctx: Ctx):
    since = ctx.rec.mark()
    f = await ask(ctx.rec, 0x01)
    if f is None:
        return FAIL, "no answer to 01: the recorder may need something first"
    ctx.shared["serial"] = f.text()
    extra = [x.describe() for x in await ctx.rec.listen(0.5, since) if x.cmd != 0x01]
    ok = f.text().isdigit()
    return (PASS if ok else FAIL), f"serial {f.text()!r}; also received: {', '.join(extra) or 'nothing'}", \
        {"serial": f.text(), "also": extra}


@check("chip-type", "read", "20 answers one byte; the app reads 0x10 as chip type 1, audio format 0 "
       "(80-byte Opus).")
async def check_chip(ctx: Ctx):
    f = await ask(ctx.rec, 0x20)
    if f is None or len(f.data) != 1:
        return FAIL, f"answer: {f.describe() if f else 'none'}"
    v = f.data[0]
    return (PASS if v == 0x10 else INFO), f"0x{v:02x}: chip type {v >> 4}, audio format {v & 0xF}", {"raw": v}


@check("firmware", "read", "12 answers the firmware version as text (1.3.3 in the capture).")
async def check_firmware(ctx: Ctx):
    f = await ask(ctx.rec, 0x12)
    if f is None:
        return FAIL, "no answer"
    ok = re.fullmatch(r"\d+(\.\d+)+", f.text()) is not None
    return (PASS if ok else FAIL), f"firmware {f.text()!r}", {"firmware": f.text()}


@check("model", "read", "29 answers a short text code (A5AB in the capture).")
async def check_model(ctx: Ctx):
    f = await ask(ctx.rec, 0x29)
    if f is None:
        return FAIL, "no answer"
    return (PASS if f.text().isprintable() else FAIL), f"model {f.text()!r}", {"model": f.text()}


@check("settings", "read", "26 answers 13 bytes of settings (01 01 01 00 3c 00 00 00 1e 01 05 01 01 "
       "in the capture; fields unknown).")
async def check_settings(ctx: Ctx):
    f = await ask(ctx.rec, 0x26)
    if f is None:
        return FAIL, "no answer"
    ctx.shared["settings"] = f.data
    fields = " ".join(f"[{i}]={b:02x}" for i, b in enumerate(f.data))
    return (PASS if len(f.data) == 13 else INFO), f"{len(f.data)} bytes: {fields}", {"hex": f.data.hex()}


@check("state", "read", "0f answers one byte (02 in the capture; meaning unknown).")
async def check_state(ctx: Ctx):
    f = await ask(ctx.rec, 0x0F)
    if f is None:
        return FAIL, "no answer"
    return (PASS if len(f.data) == 1 else INFO), f"value {f.data.hex()}", {"hex": f.data.hex()}


@check("battery", "read", "0e answers the battery level, one byte 0-100 (%).")
async def check_battery(ctx: Ctx):
    f = await ask(ctx.rec, 0x0E)
    if f is None or len(f.data) != 1:
        return FAIL, f"answer: {f.describe() if f else 'none'}"
    return (PASS if f.data[0] <= 100 else FAIL), f"{f.data[0]} %", {"percent": f.data[0]}


@check("storage", "read", "0b answers twice: 0c (free) and 0d (total), 4-byte big-endian, in MB; "
       "free <= total.")
async def check_storage(ctx: Ctx):
    since = ctx.rec.mark()
    await ctx.rec.send(0x0B)
    free = await ctx.rec.wait_for({0x0C}, since, 3.0)
    total = await ctx.rec.wait_for({0x0D}, since, 3.0)
    if not free or not total:
        return FAIL, f"free {'ok' if free else 'missing'}, total {'ok' if total else 'missing'}"
    a, b = int.from_bytes(free.data, "big"), int.from_bytes(total.data, "big")
    return (PASS if a <= b else FAIL), f"free {a}, total {b} (MB if the guess holds: {b / 1024:.1f} GB)", \
        {"free": a, "total": b}


@check("clock", "read", "30 answers the recorder's clock as YYYYMMDDHHMMSS (local time).")
async def check_clock(ctx: Ctx):
    f = await ask(ctx.rec, 0x30)
    if f is None:
        return FAIL, "no answer"
    t = parse_time(f.text())
    if t is None:
        return FAIL, f"not a time: {f.text()!r}"
    drift = (t - dt.datetime.now()).total_seconds()
    return PASS, f"{t:%Y-%m-%d %H:%M:%S}, {drift:+.0f} s from this machine", {"clock": f.text(), "drift_s": drift}


@check("list", "read", "05 answers one 05 frame per recording (14-character name + 4-byte big-endian "
       "size), then 06. Names are start times; size / 4000 is the duration in seconds.")
async def check_list(ctx: Ctx):
    entries, ended = await ctx.rec.list_files()
    ctx.shared["files"] = entries
    bad = [n for n, _ in entries if parse_time(n) is None]
    lines = [f"{n} {s} B ({s / BYTES_PER_SECOND:.1f} s)" for n, s in entries]
    status = PASS if ended and not bad else FAIL
    detail = f"{len(entries)} recordings{'' if ended else ', no 06 at the end'}" + \
             (f"; names that aren't times: {bad}" if bad else "") + ("\n      " + "\n      ".join(lines) if lines else "")
    return status, detail, {"files": entries, "ended": ended}


def pick_file(ctx: Ctx) -> tuple[str, int] | None:
    files = ctx.shared.get("files") or []
    if ctx.args.file:
        return next(((n, s) for n, s in files if n == ctx.args.file), (ctx.args.file, -1))
    sized = [f for f in files if f[1] > 0]
    return min(sized, key=lambda f: f[1]) if sized else None


@check("download", "read", "07 <name><offset> answers 07 <name><size>, then the file comes on the file "
       "data characteristic, then 09. The bytes received equal the size. Whether the app's 69 00 / "
       "66 00 before it are needed is tested: first without them.")
async def check_download(ctx: Ctx):
    target = pick_file(ctx)
    if target is None:
        return SKIP, "no recording on the recorder (make a short one and run again)"
    name, listed = target
    r = await ctx.rec.download(name, prep=False)
    tried = ["without 69/66"]
    if not r.get("received"):
        why = "no answer to 07" if not r["reply"] else f"no data ({r.get('error') or r.get('done') or 'no 09'})"
        ctx.log("check", f"without 69/66: {why}; trying the app's way")
        r = await ctx.rec.download(name, prep=True)
        tried.append("with 69/66")
    data = r.pop("data", b"")
    ctx.shared["download"] = (name, data, r)
    if data:
        (ctx.out / f"{name}.bin").write_bytes(data)
    if not r["reply"]:
        return FAIL, f"no answer to 07 ({', '.join(tried)})", r
    if "error" in r:
        return FAIL, f"07 answered fc {r['error']} ({', '.join(tried)})", r
    problems = []
    if r["echo"] != name:
        problems.append(f"echoed name {r['echo']!r}")
    if listed >= 0 and r["size"] != listed:
        problems.append(f"size {r['size']} but the list said {listed}")
    if r["received"] != r["size"]:
        problems.append(f"received {r['received']} of {r['size']} B")
    if not r["done"] or not r["done"].startswith("09"):
        problems.append(f"ended with {r['done'] or 'no done frame'}")
    needed = "69/66 not needed" if len(tried) == 1 else "needed 69/66" if r["received"] else "failed both ways"
    detail = (f"{name}: {r['received']}/{r['size']} B in {r['seconds']} s ({r['bytes_per_second']} B/s), "
              f"chunks {r['chunk_sizes']}, {needed}" + (f"; problems: {'; '.join(problems)}" if problems else ""))
    r["prep_needed"] = len(tried) > 1
    return (FAIL if problems else PASS), detail, r


@check("audio", "read", "A recording is back-to-back 80-byte Opus packets, 20 ms each, 16 kHz mono "
       "(TOC 4b), 4000 B/s; decodes to 320 samples per packet.")
async def check_audio(ctx: Ctx):
    if "download" not in ctx.shared or not ctx.shared["download"][1]:
        return SKIP, "no downloaded file"
    name, data, _ = ctx.shared["download"]
    rep = audio_report(data, ctx.out / f"{name}.wav")
    problems = []
    if rep.get("samples_per_packet") not in (None, 0, SAMPLE_RATE * FRAME_MS // 1000):
        problems.append(f"{rep['samples_per_packet']} samples per packet")
    if rep.get("decode_failed"):
        problems.append(f"{rep['decode_failed']} packets didn't decode")
    detail = (f"{rep['packets']} packets ({rep['seconds_by_packets']} s; size says {rep['seconds_by_size']} s), "
              f"TOCs {rep['tocs']}, re-aligned at {rep['resyncs_at'] or 'nowhere'}, {rep['bytes_left']} B left; "
              + (f"decoded {rep['decoded']}, wrote {rep.get('wav')}" if "decoded" in rep else rep["decode"]))
    return (FAIL if problems else PASS if "decoded" in rep else INFO), \
        detail + (f"; problems: {'; '.join(problems)}" if problems else ""), rep


@check("resume", "read", "The offset in 07 resumes a download: from offset N the recorder sends the "
       "file's bytes from N on. (Is the size in the answer the whole file or what's left?)")
async def check_resume(ctx: Ctx):
    if "download" not in ctx.shared or not ctx.shared["download"][1]:
        return SKIP, "no downloaded file to compare with"
    name, full, first = ctx.shared["download"]
    offset = len(full) // 2 // SLOT * SLOT
    if offset == 0:
        return SKIP, "file too short"
    r = await ctx.rec.download(name, offset=offset, prep=first.get("prep_needed", False))
    data = r.pop("data", b"")
    if not r["reply"] or "error" in r:
        return FAIL, f"answer: {r.get('error') or 'none'}", r
    if data == full[offset:]:
        verdict = "the bytes from the offset on, exactly"
        status = PASS
    elif data == full:
        verdict, status = "the whole file again: the offset is ignored", FAIL
    else:
        same = next((i for i, (a, b) in enumerate(zip(data, full[offset:])) if a != b), min(len(data), len(full) - offset))
        verdict, status = f"{len(data)} B, matching the file from the offset for {same} B", FAIL
    size_means = "whole file" if r["size"] == len(full) else "what's left" if r["size"] == len(full) - offset else "?"
    return status, f"offset {offset}: received {verdict}; size field {r['size']} ({size_means})", r


@check("explore", "explore", "Unknown commands: 36, 69 00, 66 00, 22 with the value already set, and an "
       "unknown command 7e. Reports every answer within 1.5 s and whether the settings (26) changed.")
async def check_explore(ctx: Ctx):
    rec = ctx.rec
    before = await ask(rec, 0x26)
    if before is None:
        return FAIL, "no settings answer to compare with"
    current = before.data[3:5]  # 22 00 3c in the capture; the settings had 00 3c at [3:5]
    probes = [(0x36, b""), (0x69, b"\x00"), (0x66, b"\x00"), (0x22, current), (0x7E, b"")]
    found = {}
    for cmd, data in probes:
        since = rec.mark()
        await rec.send(cmd, data)
        got = [f.describe() for f in await rec.listen(1.5, since)]
        found[f"{cmd:02x} {data.hex()}".strip()] = got
        ctx.log("explore", f"{cmd:02x} {data.hex()} -> {', '.join(got) or 'nothing'}")
    after = await ask(rec, 0x26)
    changed = after is None or after.data != before.data
    detail = "; ".join(f"{k} -> {', '.join(v) or 'nothing'}" for k, v in found.items())
    detail += f"; settings {'CHANGED: ' + (after.data.hex() if after else 'no answer') if changed else 'unchanged'}"
    return INFO, detail, {"answers": found, "settings_before": before.data.hex(),
                          "settings_after": after.data.hex() if after else None}


@check("set-clock", "write", "02 <YYYYMMDDHHMMSS> sets the clock and answers 02; 30 then reads it back.")
async def check_set_clock(ctx: Ctx):
    now = dt.datetime.now()
    ack = await ask(ctx.rec, 0x02, now.strftime("%Y%m%d%H%M%S").encode())
    f = await ask(ctx.rec, 0x30)
    t = parse_time(f.text()) if f else None
    if ack is None or t is None:
        return FAIL, f"ack {'ok' if ack else 'missing'}, read back {f.describe() if f else 'nothing'}"
    off = (t - dt.datetime.now()).total_seconds()
    return (PASS if abs(off) <= 2 else FAIL), f"set {now:%H:%M:%S}, reads back {t:%H:%M:%S} ({off:+.0f} s)", \
        {"offset_s": off}


@check("delete-missing", "write", f"Deleting a recording that doesn't exist ({MISSING_NAME}) answers fc "
       "(an error), and changes nothing.")
async def check_delete_missing(ctx: Ctx):
    before, _ = await ctx.rec.list_files()
    await ctx.rec.send(0x34, b"\x00\x01")
    await asyncio.sleep(0.2)
    f = await ask(ctx.rec, 0x0A, MISSING_NAME.encode(), reply={0x0A, 0xFC})
    after, _ = await ctx.rec.list_files()
    same = before == after
    if f is None:
        return FAIL, f"no answer; list {'unchanged' if same else 'CHANGED'}"
    return (PASS if f.cmd == 0xFC and same else FAIL), f"answer {f.describe()}; list {'unchanged' if same else 'CHANGED'}"


@check("delete", "write", "34 00 01, then 0a <name>, deletes the recording and answers 0a; the list no "
       "longer has it. (--no-34 tests whether 34 is needed.)")
async def check_delete(ctx: Ctx):
    name = ctx.args.delete
    if not name:
        return SKIP, "no --delete NAME given"
    before, _ = await ctx.rec.list_files()
    if name not in [n for n, _ in before]:
        return SKIP, f"{name} isn't on the recorder"
    if not ctx.args.no_34:
        await ctx.rec.send(0x34, b"\x00\x01")
        await asyncio.sleep(0.2)
    f = await ask(ctx.rec, 0x0A, name.encode(), reply={0x0A, 0xFC})
    after, _ = await ctx.rec.list_files()
    gone = name not in [n for n, _ in after]
    others = [e for e in before if e[0] != name] == after
    detail = (f"answer {f.describe() if f else 'none'}; {name} {'gone' if gone else 'STILL THERE'}; "
              f"other recordings {'unchanged' if others else 'CHANGED'}; 34 {'skipped' if ctx.args.no_34 else 'sent'}")
    return (PASS if f is not None and f.cmd == 0x0A and gone and others else FAIL), detail


@check("live", "live", "Recording on the recorder sends 03 <name> at the start, 3b 00 00 while it "
       "records, the audio live on 0011201a, 10 at the stop and 04 <name><size> when saved; the live "
       "audio is the same length as the saved file, which then shows in the list.")
async def check_live(ctx: Ctx):
    rec, wait = ctx.rec, ctx.args.live
    since = rec.mark()
    rec.live.clear()
    ctx.log("live", f"NOW: start a recording on the recorder, record ~10 s, stop it. Waiting up to {wait:g} s.")
    start = await rec.wait_for({0x03}, since, wait)
    if start is None:
        return FAIL, "no 03 (recording started) came"
    ctx.log("live", f"started: {start.text()}")
    stop = await rec.wait_for({0x10}, since, wait)
    saved = await rec.wait_for({0x04}, since, 10.0)
    await asyncio.sleep(0.5)
    frames = rec.frames[since:]
    status_frames = Counter(f.data.hex() for f in frames if f.cmd == 0x3B)
    live = bytes(rec.live)
    if live:
        (ctx.out / f"live-{start.text()}.bin").write_bytes(live)
    detail = [f"started {start.text()}", f"stopped {'yes' if stop else 'NO'}",
              f"3b frames {dict(status_frames)}", f"live audio {len(live)} B"]
    data = {"events": [f.describe() for f in frames]}
    ok = stop is not None and saved is not None
    if saved:
        name, size = parse_entry(saved.data)
        detail.append(f"saved {name} {size} B")
        ok = ok and name == start.text() and size == len(live)
        entries, _ = await rec.list_files()
        listed = name in [n for n, _ in entries]
        detail.append(f"{'in' if listed else 'NOT in'} the list")
        ok = ok and listed
    else:
        detail.append("no 04 (saved)")
    if live:
        rep = audio_report(live, ctx.out / f"live-{start.text()}.wav")
        detail.append(f"{rep['packets']} packets, re-aligned at {rep['resyncs_at'] or 'nowhere'}")
        data["audio"] = rep
    return (PASS if ok else FAIL), "; ".join(detail), data


# --- Running --------------------------------------------------------------------------------

T0 = time.monotonic()


class Logger:
    def __init__(self, out: Path):
        self.frames = open(out / "frames.log", "w")

    def log(self, kind: str, text: str) -> None:
        line = f"[{time.monotonic() - T0:7.2f}] {kind:<8} {text}"
        print(line, flush=True)
        self.frames.write(line + "\n")
        self.frames.flush()

    def frame(self, direction: str, f: Frame) -> None:
        self.frames.write(f"[{f.t - T0:7.2f}] {'phone>' if direction == '>' else 'rec<':<8} {f.describe()}\n")
        self.frames.flush()


def select(args) -> list[Check]:
    names = [c.name for c in CHECKS]
    if args.checks:
        wanted = [n.strip() for n in args.checks.split(",") if n.strip()]
        unknown = [n for n in wanted if n not in names]
        if unknown:
            raise SystemExit(f"unknown checks: {', '.join(unknown)} (see --list-checks)")
        chosen = [c for c in CHECKS if c.name in wanted]
    else:
        groups = {"read"} | ({"explore"} if args.explore else set()) | ({"write"} if args.allow_write else set()) \
            | ({"live"} if args.live else set())
        chosen = [c for c in CHECKS if c.group in groups and (c.name != "delete" or args.delete)]
    skip = {n.strip() for n in (args.skip or "").split(",") if n.strip()}
    chosen = [c for c in chosen if c.name not in skip]
    blocked = [c.name for c in chosen if c.group == "write" and not args.allow_write]
    if blocked:
        raise SystemExit(f"{', '.join(blocked)} change the recorder: add --allow-write")
    if any(c.name == "live" for c in chosen) and not args.live:
        args.live = 60.0
    return chosen


async def run(rec: Airec, checks: list[Check], ctx: Ctx) -> list[Result]:
    results = []
    for c in checks:
        ctx.log("check", f"--- {c.name}: {c.claim}")
        try:
            got = await c.fn(ctx)
            status, detail, data = (*got, {}) if len(got) == 2 else got
        except Exception as e:  # noqa: BLE001 - one failing check shouldn't stop the others
            status, detail, data = ERROR, f"{type(e).__name__}: {e}", {"traceback": traceback.format_exc()}
        results.append(Result(c.name, status, c.claim, detail, data))
        ctx.log(status, f"{c.name}: {detail}")
        if rec.disconnected:
            ctx.log("stop", "the recorder disconnected; the remaining checks can't run")
            break
    return results


def summary(results: list[Result]) -> str:
    width = max((len(r.name) for r in results), default=0)
    return "\n".join(f"  {r.status:<5} {r.name:<{width}}  {r.detail.splitlines()[0][:150]}" for r in results)


async def main_async(args) -> int:
    out = Path(args.out or f"airec-{dt.datetime.now():%Y%m%d-%H%M%S}")
    out.mkdir(parents=True, exist_ok=True)
    logger = Logger(out)
    checks = select(args)
    logger.log("start", f"checks: {', '.join(c.name for c in checks)}; saving to {out}/")
    rec = BleAirec(args.address, args.name, args.scan_timeout, on_frame=logger.frame, on_event=logger.log)
    results: list[Result] = []
    try:
        async with rec:
            ctx = Ctx(rec, args, out, logger.log)
            results = await run(rec, checks, ctx)
            await asyncio.sleep(0.3)
    except (ConnectionError, OSError, asyncio.TimeoutError) as e:
        logger.log("ERROR", f"{type(e).__name__}: {e}")
    print("\n=== Results\n" + summary(results))
    if rec.parser.skipped:
        print(f"\n  {rec.parser.skipped} reply bytes didn't belong to any frame (see frames.log)")
    report = {
        "when": dt.datetime.now().isoformat(timespec="seconds"),
        "recorder": {"name": rec.device_name, "address": rec.device_address, "mtu": rec.mtu},
        "results": [asdict(r) for r in results],
        "unparsed_reply_bytes": rec.parser.skipped,
    }
    (out / "report.json").write_text(json.dumps(report, indent=2, default=lambda o: o.hex() if isinstance(o, bytes) else str(o)))
    print(f"\nSaved to {out}/: report.json, frames.log and any downloaded audio.")
    return 0 if results and all(r.status in (PASS, INFO, SKIP) for r in results) else 1


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="airec-probe", description=__doc__.split("\n\n")[0],
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Examples:\n"
               "  airec-probe                                   # read-only checks, finds AIREC by name\n"
               "  airec-probe 64:48:55:51:81:5E --explore\n"
               "  airec-probe --allow-write --delete 20261015180213\n"
               "  airec-probe --checks list,download,audio --file 20261015183232\n"
               "  airec-probe --live 90                         # then record on the recorder")
    p.add_argument("address", nargs="?", help="the recorder's Bluetooth address (default: the first "
                                              "device whose name matches --name)")
    p.add_argument("--name", default=NAME_RE, help=f"name regex to find the recorder by (default {NAME_RE!r})")
    p.add_argument("--scan-timeout", type=float, default=20.0, help="seconds to look for it (default 20)")
    p.add_argument("--checks", help="run only these, comma-separated (see --list-checks)")
    p.add_argument("--skip", help="leave these out, comma-separated")
    p.add_argument("--list-checks", action="store_true", help="list the checks and what each assumes")
    p.add_argument("--file", help="recording to download (default: the smallest)")
    p.add_argument("--explore", action="store_true", help="send the commands we don't understand yet")
    p.add_argument("--allow-write", action="store_true",
                   help="allow checks that change the recorder: set its clock, delete a missing file")
    p.add_argument("--delete", metavar="NAME", help="with --allow-write: delete this recording (it's gone)")
    p.add_argument("--no-34", action="store_true", help="delete without sending 34 00 01 first")
    p.add_argument("--live", type=float, metavar="SECONDS",
                   help="wait this long for a recording made on the recorder, and check its events and audio")
    p.add_argument("--out", help="output folder (default airec-<time>)")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    if args.list_checks:
        for c in CHECKS:
            print(f"{c.name:<15} [{c.group}] {c.claim}")
        return 0
    if args.delete and not args.allow_write:
        build_parser().error("--delete needs --allow-write")
    try:
        return asyncio.run(main_async(args))
    except KeyboardInterrupt:
        print("\ninterrupted")
        return 130


if __name__ == "__main__":
    import sys
    sys.exit(main())
