#!/usr/bin/env python3
"""Captures the official Pocket app talking to a recorder, to work out its protocols.

Runs on a Linux laptop with an Android phone attached over adb (USB debugging on). Scenarios:

- `activation`: the app activating (setting up) a recorder, to work out how activation works
  and where the session key comes from.
- `quick-transfer`: the app's Quick Transfer (Wi-Fi), to work out the WiFi transfer.

Each captures, all at once:

- Bluetooth: the phone's HCI snoop log, streamed live from the phone's btsnoop socket (TCP 8872)
  through `adb forward`. It's decoded as it arrives: connections, pairing (SMP), encryption,
  the recorder's advertising, the GATT layout, and every ATT read, write, notification and
  indication. A bug report at the end also pulls the log file kept on the phone, in case the
  socket isn't there (release builds of Android often leave it out).
- App log: `adb logcat`, for the app's own messages.
- WiFi: this laptop's card in monitor mode. It hops the 2.4 GHz channels until a frame from the
  recorder shows up (its known AP MAC, or any MAC with the recorder's WiFi OUI), then stays on
  that channel. The recorder's probe requests are logged, which shows the networks it looks
  for. Traffic is encrypted (WPA2-PSK); decrypting needs the 4-way handshake, so the capture
  must be running before anything joins.

Everything goes into capture-<scenario>-<time>/, with a timeline.log of everything printed plus
the full values. While it runs, type a note and Enter to put a marker in the timeline. The
laptop's WiFi is put back afterwards, also after Ctrl-C. Standard library only; run it with the
system python3, not inside a sandbox.

    python3 capture_app.py activation
    python3 capture_app.py quick-transfer
    python3 capture_app.py decode capture-…/phone-live.btsnoop     # no phone needed
"""
from __future__ import annotations

import argparse
import getpass
import re
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import zipfile
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import BinaryIO, Callable, Iterator

try:
    import termios
except ImportError:  # `decode` also works where there's no termios
    termios = None

RECORDER_BSSID = "a4:c1:38:81:90:50"
RECORDER_WIFI_OUI = "a4:c1:38"
RECORDER_BT_ADDR = "F4:4C:26:17:17:FF"
RECORDER_NAME = r"^PKT"
APP_PACKAGE = "com.heypocket.app"
SNOOP_PORT = 8872
FREQS_24 = [2412 + 5 * i for i in range(13)]

T0 = time.monotonic()
_print_lock = threading.Lock()
_timeline = None  # open file: everything said, unredacted, with full values
# adb puts a terminal it can reach into raw mode and reads the keys itself, so every child gets
# stdin=DEVNULL, and the terminal settings from startup are put back before each prompt.
_TTY = termios.tcgetattr(sys.stdin) if termios and sys.stdin.isatty() else None


def restore_tty() -> None:
    if _TTY is not None:
        termios.tcsetattr(sys.stdin, termios.TCSANOW, _TTY)


def prompt(text: str) -> str:
    restore_tty()
    return input(text)


def say(kind: str, text: str, detail: str | None = None, quiet: bool = False) -> None:
    """Prints a line (redacted) and adds it to the timeline (not redacted, plus `detail`).

    `quiet` lines only go to the timeline.
    """
    stamp = f"[{time.monotonic() - T0:7.1f}] {kind:<8}"
    with _print_lock:
        if not quiet:
            print(f"{stamp} {redact(text)}", flush=True)
        if _timeline:
            _timeline.write(f"{stamp} {text}\n" + (f"{'':19}{detail}\n" if detail else ""))
            _timeline.flush()


def step(text: str) -> None:
    with _print_lock:
        print(f"\n=== {text}", flush=True)


def ask(text: str) -> None:
    """Tells the user what to do now and waits for Enter."""
    with _print_lock:
        print(f"\n>>> {text}", flush=True)
    prompt(">>> Press Enter when done. ")


def mark_until_done(text: str) -> None:
    """Tells the user what to do; every line they type goes into the timeline until 'done'."""
    with _print_lock:
        print(f"\n>>> {text}\n>>> Type a note and Enter to mark the timeline; 'done' and Enter "
              "when finished.\n", flush=True)
    while True:
        line = prompt("").strip()
        if line.lower() in ("done", "q", "quit"):
            return
        if line:
            say("MARK", line)


def run(cmd: list[str], *, sudo: bool = False, check: bool = True, timeout: float = 30) -> str:
    if sudo:
        cmd = ["sudo", "-n", *cmd]
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)
    if check and p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed: {(p.stderr or p.stdout).strip()}")
    return p.stdout


def redact(text: str) -> str:
    text = re.sub(r"((?:APP|MCU)&SK&)[^&\s]{4,}", r"\1<redacted>", text)
    return re.sub(r"(MCU&WIFI&[^&]+&)\S+", r"\1<redacted>", text)


# --- Bluetooth: decoding HCI ---------------------------------------------------------------

BTSNOOP_UNIX_OFFSET_US = 0x00DCDDB30F2F8000  # btsnoop time is µs since year 0

ATT_OPS = {
    0x01: "error", 0x02: "MTU request", 0x03: "MTU response", 0x04: "find information",
    0x06: "find by type value", 0x08: "read by type", 0x0A: "read", 0x0C: "read blob",
    0x0E: "read multiple", 0x10: "read by group type", 0x12: "write", 0x16: "prepare write",
    0x18: "execute write", 0x20: "read multiple variable", 0x52: "write command",
    0xD2: "signed write",
}
ATT_ERRORS = {
    0x01: "invalid handle", 0x02: "read not permitted", 0x03: "write not permitted",
    0x05: "insufficient authentication", 0x06: "request not supported", 0x07: "invalid offset",
    0x08: "insufficient authorization", 0x0A: "attribute not found", 0x0D: "invalid length",
    0x0E: "unlikely error", 0x0F: "insufficient encryption",
}
SMP_OPS = {
    0x01: "pairing request", 0x02: "pairing response", 0x03: "pairing confirm",
    0x04: "pairing random", 0x05: "pairing failed", 0x06: "encryption information (LTK)",
    0x07: "central identification", 0x08: "identity information (IRK)",
    0x09: "identity address", 0x0A: "signing information", 0x0B: "security request",
    0x0C: "public key", 0x0D: "DHKey check", 0x0E: "keypress",
}
SMP_FAILURES = {
    0x01: "passkey entry failed", 0x02: "OOB not available", 0x03: "authentication requirements",
    0x04: "confirm value failed", 0x05: "pairing not supported", 0x06: "encryption key size",
    0x07: "command not supported", 0x08: "unspecified", 0x09: "repeated attempts",
    0x0B: "DHKey check failed", 0x0C: "numeric comparison failed",
}
IO_CAPS = ["DisplayOnly", "DisplayYesNo", "KeyboardOnly", "NoInputNoOutput", "KeyboardDisplay"]
GATT_NAMES = {
    "2800": "primary service", "2801": "secondary service", "2803": "characteristic",
    "2902": "CCCD", "2a00": "device name", "2a19": "battery level", "2a24": "model number",
    "2a25": "serial number", "2a26": "firmware revision", "2a27": "hardware revision",
    "2a28": "software revision", "2a29": "manufacturer", "2a05": "service changed",
}
PROPS = [(0x02, "read"), (0x04, "write-no-rsp"), (0x08, "write"), (0x10, "notify"),
         (0x20, "indicate"), (0x40, "signed-write")]
DISCOVERY = (0x04, 0x06, 0x08, 0x10)  # requests whose "not found" error just ends a search
NOTIFY_SHOWN = 20  # binary notifications per characteristic shown one by one, then every 500th

Emit = Callable[..., None]  # emit(kind, text, detail=None, quiet=False)


def u16(b: bytes) -> int:
    return int.from_bytes(b[:2], "little")


def bdaddr(b: bytes) -> str:
    return ":".join(f"{x:02X}" for x in reversed(b))


def uuid_str(b: bytes) -> str:
    """A UUID from the air (little-endian): 16-bit ones as 4 hex digits, else the full form."""
    if len(b) == 2:
        return f"{u16(b):04x}"
    if len(b) == 16:
        h = b[::-1].hex()
        if h.endswith("00001000800000805f9b34fb") and h.startswith("0000"):
            return h[4:8]
        return f"{h[:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:]}"
    return b.hex()


def is_text(value: bytes) -> bool:
    text = value.rstrip(b"\0\r\n")
    return bool(text) and all(32 <= c < 127 for c in text)


def show(value: bytes, limit: int = 48) -> str:
    """The value as text when it reads as text, else as hex; cut to `limit` bytes."""
    if is_text(value):
        return value.rstrip(b"\0\r\n").decode("ascii")
    if not value:
        return "(empty)"
    cut = value[:limit].hex()
    return f"{cut}{'…' if len(value) > limit else ''} ({len(value)} B)"


def parse_ad(data: bytes) -> dict[str, list[str]]:
    """Advertising data structures: names, UUIDs, manufacturer and service data, flags."""
    out: dict[str, list[str]] = {}
    i = 0
    while i < len(data):
        n = data[i]
        if n == 0 or i + 1 + n > len(data):
            break
        t, v = data[i + 1], data[i + 2:i + 1 + n]
        i += 1 + n
        if t in (0x08, 0x09):
            out.setdefault("name", []).append(v.decode("utf-8", "replace"))
        elif t in (0x02, 0x03):
            out.setdefault("uuids", []).extend(uuid_str(v[j:j + 2]) for j in range(0, len(v) - 1, 2))
        elif t in (0x06, 0x07):
            out.setdefault("uuids", []).extend(uuid_str(v[j:j + 16]) for j in range(0, len(v) - 15, 16))
        elif t == 0xFF and len(v) >= 2:
            out.setdefault("mfr", []).append(f"{u16(v):04x}:{v[2:].hex()}")
        elif t == 0x16 and len(v) >= 2:
            out.setdefault("svcdata", []).append(f"{u16(v):04x}:{v[2:].hex()}")
        elif t == 0x01 and v:
            out.setdefault("flags", []).append(f"{v[0]:02x}")
    return out


@dataclass
class Conn:
    """What's known about one link: its peer and its GATT layout as discovered on the air."""
    handle: int
    addr: str | None = None
    uuids: dict[int, str] = field(default_factory=dict)  # attribute handle -> UUID
    chars: dict[int, str] = field(default_factory=dict)  # characteristic value handle -> UUID
    pending: dict[bool, tuple] = field(default_factory=dict)  # request outstanding, per sender
    prepared: dict[int, bytearray] = field(default_factory=dict)  # queued (long) writes
    notifies: dict[int, int] = field(default_factory=dict)  # binary notifications per handle


class HciDecoder:
    """Decodes H4 HCI packets from a btsnoop log into readable events."""

    def __init__(self, emit: Emit, name_re: str = RECORDER_NAME, addrs: tuple[str, ...] = (RECORDER_BT_ADDR,)):
        self.emit = emit
        self.name_re = re.compile(name_re)
        self.recorders = {a.upper() for a in addrs}  # Bluetooth addresses known to be a recorder
        self.names: dict[str, str] = {}
        self.seen: set[str] = set()  # recorders seen advertising or connected
        self.conns: dict[int, Conn] = {}
        self.frags: dict[tuple[int, bool], bytearray] = {}
        self.adv_last: dict[tuple[str, bool], tuple[str, float]] = {}
        self.ts = 0.0  # seconds, from the btsnoop record being decoded
        self.packets = 0

    # btsnoop flags: bit 0 set = received by the host (from the controller), bit 1 = command/event
    def packet(self, flags: int, pkt: bytes, ts_us: int = 0, datalink: int = 1002) -> None:
        self.packets += 1
        self.ts = ts_us / 1e6
        if datalink == 1001:  # no H4 type byte: rebuild it from the flags
            pkt = bytes([(0x04 if flags & 1 else 0x01) if flags & 2 else 0x02]) + pkt
        if not pkt:
            return
        sent = not flags & 1
        try:
            if pkt[0] == 0x01:
                self._command(pkt[1:])
            elif pkt[0] == 0x04:
                self._event(pkt[1:])
            elif pkt[0] == 0x02:
                self._acl(pkt[1:], sent)
        except (IndexError, struct.error, ValueError) as e:  # a truncated or odd packet
            self.emit("bt", f"couldn't decode a packet ({e}): {pkt[:32].hex()}", quiet=True)

    def _conn(self, handle: int) -> Conn:
        return self.conns.setdefault(handle, Conn(handle))

    def _who(self, addr: str | None) -> str:
        if not addr:
            return "?"
        name = self.names.get(addr)
        return f"{name} ({addr})" if name else addr

    def _tag(self, c: Conn) -> str:
        return self.names.get(c.addr or "") or c.addr or f"link 0x{c.handle:03x}"

    def _handle_name(self, c: Conn, h: int) -> str:
        uuid = c.chars.get(h) or c.uuids.get(h)
        if uuid == "2902":
            owner = max((v for v in c.chars if v < h), default=None)
            return f"CCCD of {self._uuid_name(c.chars[owner])}" if owner is not None else f"CCCD 0x{h:04x}"
        return f"{self._uuid_name(uuid)} (0x{h:04x})" if uuid else f"0x{h:04x}"

    @staticmethod
    def _uuid_name(uuid: str) -> str:
        return GATT_NAMES.get(uuid) or (uuid[:8] if len(uuid) > 8 else uuid)

    # --- HCI commands and events

    def _command(self, b: bytes) -> None:
        op, p = u16(b), b[3:3 + b[2]]
        if op == 0x200D:  # LE Create Connection
            target = "its accept list" if p[4] else self._who(bdaddr(p[6:12]))
            self.emit("bt", f"phone connects to {target}")
        elif op == 0x2043:  # LE Extended Create Connection
            target = "its accept list" if p[0] else self._who(bdaddr(p[3:9]))
            self.emit("bt", f"phone connects to {target}")
        elif op == 0x0406:  # Disconnect
            self.emit("bt", f"phone disconnects {self._tag(self._conn(u16(p) & 0xFFF))} (reason 0x{p[2]:02x})")
        elif op == 0x2019:  # LE Enable Encryption: the phone has a key, so they're bonded
            self.emit("bt", f"phone starts encryption with a stored key on {self._tag(self._conn(u16(p) & 0xFFF))}")
        elif op in (0x200C, 0x2042):  # LE Set (Extended) Scan Enable
            self.emit("bt", f"phone scanning {'on' if p[0] else 'off'}", quiet=True)

    def _event(self, b: bytes) -> None:
        code, p = b[0], b[2:2 + b[1]]
        if code == 0x3E:
            self._le_meta(p[0], p[1:])
        elif code == 0x03 and len(p) >= 11 and p[0] == 0:  # classic Connection Complete
            c = self.conns[u16(p[1:3]) & 0xFFF] = Conn(u16(p[1:3]) & 0xFFF, bdaddr(p[3:9]))
            self.emit("bt", f"classic link to {self._who(c.addr)} (0x{c.handle:03x})")
        elif code == 0x05 and len(p) >= 4:  # Disconnection Complete
            c = self.conns.pop(u16(p[1:3]) & 0xFFF, None)
            name = self._tag(c) if c else f"link 0x{u16(p[1:3]) & 0xFFF:03x}"
            self.emit("bt", f"disconnected from {name} (reason 0x{p[3]:02x})")
        elif code in (0x08, 0x59) and len(p) >= 4:  # Encryption Change (v1, v2)
            c = self._conn(u16(p[1:3]) & 0xFFF)
            state = f"failed (status 0x{p[0]:02x})" if p[0] else "on" if p[3] else "off"
            self.emit("bt", f"encryption {state} on {self._tag(c)}")

    def _le_meta(self, sub: int, q: bytes) -> None:
        if sub in (0x01, 0x0A):  # LE (Enhanced) Connection Complete
            handle, addr = u16(q[1:3]) & 0xFFF, bdaddr(q[5:11])
            if q[0]:
                self.emit("bt", f"connection to {self._who(addr)} failed (status 0x{q[0]:02x})")
                return
            self.conns[handle] = Conn(handle, addr)
            if addr in self.recorders:
                self.seen.add(addr)
            role = "central" if q[3] == 0 else "peripheral"
            self.emit("bt", f"connected to {self._who(addr)} (link 0x{handle:03x}, phone is {role})")
        elif sub == 0x02:  # LE Advertising Report
            i = 1
            for _ in range(q[0]):
                n = q[i + 8]
                self._adv(bdaddr(q[i + 2:i + 8]), q[i + 9:i + 9 + n], scan_rsp=q[i] == 0x04)
                i += 10 + n
        elif sub == 0x0D:  # LE Extended Advertising Report
            i = 1
            for _ in range(q[0]):
                n = q[i + 23]
                self._adv(bdaddr(q[i + 3:i + 9]), q[i + 24:i + 24 + n], scan_rsp=bool(u16(q[i:i + 2]) & 0x08))
                i += 24 + n

    def _adv(self, addr: str, data: bytes, scan_rsp: bool) -> None:
        ad = parse_ad(data)
        name = (ad.get("name") or [None])[0]
        if name and self.name_re.search(name):
            self.recorders.add(addr)
        if name:
            self.names.setdefault(addr, name)
        if addr not in self.recorders:
            return
        self.seen.add(addr)
        summary = " ".join(f"{k}={','.join(v)}" for k, v in sorted(ad.items())) or "(no data)"
        key = (addr, scan_rsp)
        last = self.adv_last.get(key)
        if last and last[0] == summary:
            return
        # Every change goes to the timeline; the screen gets at most one a second per device.
        loud = last is None or self.ts - last[1] >= 1
        self.adv_last[key] = (summary, self.ts if loud else last[1])
        what = "scan response" if scan_rsp else "advertises"
        self.emit("adv", f"{self._who(addr)} {what}: {summary}", quiet=not loud)

    # --- ACL, L2CAP

    def _acl(self, b: bytes, sent: bool) -> None:
        hf, n = struct.unpack("<HH", b[:4])
        handle, pb, data = hf & 0xFFF, (hf >> 12) & 0x3, b[4:4 + n]
        key = (handle, sent)
        if pb == 0x1:  # continuation fragment
            buf = self.frags.get(key)
            if buf is None:
                return
            buf += data
        else:
            buf = bytearray(data)
        if len(buf) < 4 or len(buf) < u16(buf) + 4:
            self.frags[key] = buf
            return
        self.frags.pop(key, None)
        cid, payload = u16(buf[2:4]), bytes(buf[4:4 + u16(buf)])
        if not payload:
            return
        if cid == 0x0004:
            self._att(self._conn(handle), sent, payload)
        elif cid == 0x0006:
            self._smp(self._conn(handle), sent, payload)

    # --- ATT (GATT)

    def _att(self, c: Conn, sent: bool, a: bytes) -> None:
        who = "phone>" if sent else "device<"
        tag = self._tag(c)
        op = a[0]
        if op == 0x01:  # error
            req, h, err = a[1], u16(a[2:4]), a[4]
            c.pending.pop(not sent, None)
            self.emit(who, f"{tag} {ATT_OPS.get(req, hex(req))} {self._handle_name(c, h)} failed: "
                           f"{ATT_ERRORS.get(err, hex(err))}", quiet=err == 0x0A and req in DISCOVERY)
        elif op in (0x02, 0x03):
            self.emit(who, f"{tag} {ATT_OPS[op]} {u16(a[1:3])}")
        elif op == 0x05:  # find information response: descriptors
            size = 4 if a[1] == 1 else 18
            for j in range(2, len(a) - size + 1, size):
                c.uuids[u16(a[j:j + 2])] = uuid_str(a[j + 2:j + size])
        elif op == 0x08:
            c.pending[sent] = ("type", uuid_str(a[5:]))
        elif op == 0x09:  # read by type response
            pend = c.pending.pop(not sent, ("type", "?"))
            size = a[1]
            for j in range(2, len(a) - size + 1, size):
                h, v = u16(a[j:j + 2]), a[j + 2:j + size]
                if pend[1] == "2803" and len(v) in (5, 19):
                    vh, uuid = u16(v[1:3]), uuid_str(v[3:])
                    c.chars[vh] = c.uuids[vh] = uuid
                    props = ",".join(n for bit, n in PROPS if v[0] & bit)
                    self.emit("gatt", f"{tag} characteristic {uuid} at 0x{vh:04x} [{props}]")
                else:
                    self.emit(who, f"{tag} read {self._uuid_name(pend[1])} (0x{h:04x}): {show(v)}",
                              v.hex())
        elif op == 0x10:
            c.pending[sent] = ("group", uuid_str(a[5:]))
        elif op == 0x11:  # read by group type response: services
            size = a[1]
            for j in range(2, len(a) - size + 1, size):
                start, end, uuid = u16(a[j:j + 2]), u16(a[j + 2:j + 4]), uuid_str(a[j + 4:j + size])
                c.uuids[start] = uuid
                self.emit("gatt", f"{tag} service {uuid} at 0x{start:04x}-0x{end:04x}")
        elif op in (0x0A, 0x0C):
            c.pending[sent] = ("read", u16(a[1:3]), u16(a[3:5]) if op == 0x0C else 0)
        elif op in (0x0B, 0x0D):
            pend = c.pending.pop(not sent, ("read", None, 0))
            where = self._handle_name(c, pend[1]) if pend[1] is not None else "?"
            at = f" @{pend[2]}" if pend[2] else ""
            self.emit(who, f"{tag} read {where}{at}: {show(a[1:])}", a[1:].hex())
        elif op in (0x12, 0x52, 0xD2):
            h, v = u16(a[1:3]), a[3:] if op != 0xD2 else a[3:-12]
            self._write(c, who, h, v, "write" if op == 0x12 else "write-cmd")
        elif op == 0x16:  # prepare write: part of a long write
            c.prepared.setdefault(u16(a[1:3]), bytearray()).extend(a[5:])
        elif op == 0x18:
            if a[1]:
                for h, v in c.prepared.items():
                    self._write(c, who, h, bytes(v), "long write")
            c.prepared.clear()
        elif op in (0x1B, 0x1D):
            self._notification(c, who, u16(a[1:3]), a[3:], "notify" if op == 0x1B else "indicate")
        elif op in (0x04, 0x06, 0x13, 0x17, 0x19, 0x1E):
            pass  # discovery requests and plain acknowledgements
        else:
            self.emit(who, f"{tag} ATT 0x{op:02x}: {show(a[1:])}", a.hex(), quiet=True)

    def _write(self, c: Conn, who: str, h: int, v: bytes, verb: str) -> None:
        name = self._handle_name(c, h)
        if name.startswith("CCCD") and len(v) == 2:
            mode = {0: "unsubscribe", 1: "subscribe (notify)", 2: "subscribe (indicate)"}.get(u16(v), v.hex())
            self.emit(who, f"{self._tag(c)} {mode} {name[8:] if name.startswith('CCCD of') else name}")
            return
        self.emit(who, f"{self._tag(c)} {verb} {name}: {show(v)}", v.hex())

    def _notification(self, c: Conn, who: str, h: int, v: bytes, verb: str) -> None:
        text = show(v)
        if is_text(v):  # always shown
            self.emit(who, f"{self._tag(c)} {verb} {self._handle_name(c, h)}: {text}")
            return
        n = c.notifies[h] = c.notifies.get(h, 0) + 1
        if n <= NOTIFY_SHOWN:
            self.emit(who, f"{self._tag(c)} {verb} {self._handle_name(c, h)}: {text}", v.hex())
        elif n % 500 == 0:
            self.emit(who, f"{self._tag(c)} {n} binary {verb}s on {self._handle_name(c, h)} so far "
                           f"(latest {len(v)} B)")

    # --- SMP (pairing)

    def _smp(self, c: Conn, sent: bool, s: bytes) -> None:
        who = "phone>" if sent else "device<"
        op = s[0]
        name = SMP_OPS.get(op, f"SMP 0x{op:02x}")
        if op in (0x01, 0x02) and len(s) >= 7:
            io, oob, auth, keysize, ikeys, rkeys = s[1:7]
            flags = [n for bit, n in ((0x01, "bonding"), (0x04, "MITM"), (0x08, "secure connections"),
                                      (0x10, "keypress")) if auth & bit]
            iocap = IO_CAPS[io] if io < len(IO_CAPS) else hex(io)
            self.emit(who, f"{self._tag(c)} {name}: io={iocap}, {'+'.join(flags) or 'no flags'}, "
                           f"OOB={'yes' if oob else 'no'}, key size {keysize}, "
                           f"keys initiator 0x{ikeys:02x} responder 0x{rkeys:02x}")
        elif op == 0x05:
            self.emit(who, f"{self._tag(c)} {name}: {SMP_FAILURES.get(s[1], hex(s[1]))}")
        elif op == 0x0B:
            self.emit(who, f"{self._tag(c)} {name} (auth 0x{s[1]:02x})")
        else:
            self.emit(who, f"{self._tag(c)} {name}", s[1:].hex(), quiet=op in (0x03, 0x04, 0x0C, 0x0D))


def read_btsnoop(f: BinaryIO) -> Iterator[tuple[int, int, bytes, int]]:
    """(flags, timestamp in µs since the Unix epoch, packet, datalink) for each record."""
    header = f.read(16)
    if not header.startswith(b"btsnoop\0"):
        raise ValueError("not a btsnoop file")
    datalink = struct.unpack(">I", header[12:16])[0]
    while True:
        head = f.read(24)
        if len(head) < 24:
            return
        _orig, incl, flags, _drops, ts = struct.unpack(">IIIIq", head)
        pkt = f.read(incl)
        if len(pkt) < incl:
            return
        yield flags, ts - BTSNOOP_UNIX_OFFSET_US, pkt, datalink


def decode_file(path: Path, out, everything: bool = False) -> int:
    """Writes the decoded events of a btsnoop file to `out`; returns the packet count."""
    t0 = None

    def emit(kind, text, detail=None, quiet=False):
        if quiet and not everything:
            return
        rel = dec.ts - t0
        out.write(f"[{rel:9.3f}] {kind:<8} {text}\n" + (f"{'':20} {detail}\n" if detail and everything else ""))

    dec = HciDecoder(emit)
    with open(path, "rb") as f:
        for flags, ts, pkt, datalink in read_btsnoop(f):
            if t0 is None:
                t0 = ts / 1e6
                out.write(f"# {path.name}, starts {datetime.fromtimestamp(t0):%Y-%m-%d %H:%M:%S}\n")
            dec.packet(flags, pkt, ts, datalink)
    return dec.packets


# --- Bluetooth: btsnoop stream -------------------------------------------------------------

SNOOP_HEADER_LEN = 16


def forward_snoop(local_port: int = 0) -> int:
    """`adb forward` to the phone's snoop socket; returns the laptop port (0 picks a free one)."""
    out = adb("forward", f"tcp:{local_port}", f"tcp:{SNOOP_PORT}").strip()
    return int(out) if local_port == 0 else local_port


def connect_snoop(port: int, wait: float = 15) -> tuple[socket.socket | None, bytes, str]:
    """Connects through the adb forward and reads the btsnoop header.

    Only reading tells whether it works: `adb forward` accepts the connection even when nothing
    listens on the phone, and then closes it. Retries for `wait` seconds, as the socket opens a
    few seconds after Bluetooth starts. Returns (socket, header, "") or (None, b"", why not). The
    socket serves one client at a time, so the caller keeps this connection for the stream.
    """
    deadline = time.monotonic() + wait
    while True:
        try:
            sock = socket.create_connection(("127.0.0.1", port), timeout=3)
        except OSError as e:
            why = f"can't connect to the adb forward on laptop port {port}: {e}"
        else:
            sock.settimeout(3)
            header, why = b"", ""
            try:
                while len(header) < SNOOP_HEADER_LEN:
                    chunk = sock.recv(SNOOP_HEADER_LEN - len(header))
                    if not chunk:
                        why = ("nothing listens on the phone's port 8872 (adb accepted, then closed "
                               "the connection)")
                        break
                    header += chunk
            except socket.timeout:
                why = ("something on the phone accepted, but sent no btsnoop header within 3 s "
                       f"(got {len(header)} bytes)")
            except OSError as e:
                why = f"the connection broke while reading the header: {e}"
            if len(header) == SNOOP_HEADER_LEN:
                if header.startswith(b"btsnoop\0"):
                    return sock, header, ""
                why = f"the phone's port 8872 answers, but not with a btsnoop header: {header.hex()}"
            sock.close()
        if time.monotonic() >= deadline:
            return None, b"", why
        time.sleep(1)


class SnoopStream(threading.Thread):
    """Reads the phone's btsnoop socket, saves it as a .btsnoop file and decodes it live."""

    def __init__(self, path: Path, sock: socket.socket, header: bytes, decoder: HciDecoder):
        super().__init__(daemon=True)
        self.path, self.sock, self.header, self.decoder = path, sock, header, decoder
        self.bytes = len(header)
        self.datalink = struct.unpack(">I", header[12:16])[0]
        self.stop = threading.Event()
        self.first = threading.Event()  # set once the first packet arrived

    def run(self) -> None:
        self.sock.settimeout(1)
        buf = b""
        with self.sock, open(self.path, "wb") as out:
            out.write(self.header)
            while not self.stop.is_set():
                try:
                    data = self.sock.recv(65536)
                except socket.timeout:
                    continue
                except OSError:
                    break
                if not data:
                    break
                out.write(data)
                out.flush()
                self.bytes += len(data)
                buf = self._records(buf + data)
        if self.stop.is_set():
            say("bt", f"snoop stream stopped after {self.decoder.packets} packets")
        else:
            say("bt", f"the phone closed the snoop stream after {self.decoder.packets} packets "
                      "(Bluetooth restarted, or another client took the socket); the bug report "
                      "at the end still has the log")

    def _records(self, buf: bytes) -> bytes:
        while len(buf) >= 24:
            _orig, incl, flags, _drops, ts = struct.unpack(">IIIIq", buf[:24])
            if len(buf) < 24 + incl:
                break
            if not self.first.is_set():
                self.first.set()
                say("bt", "Bluetooth packets are arriving from the snoop socket")
            self.decoder.packet(flags, buf[24:24 + incl], ts - BTSNOOP_UNIX_OFFSET_US, self.datalink)
            buf = buf[24 + incl:]
        return buf


# --- App log -------------------------------------------------------------------------------

class Logcat(threading.Thread):
    """Saves all of adb logcat, and prints the matching lines from the app's own process."""

    def __init__(self, path: Path, interesting: re.Pattern | None):
        super().__init__(daemon=True)
        self.path, self.interesting = path, interesting
        self.proc: subprocess.Popen | None = None
        self.pids: set[str] = set()
        self.stop = threading.Event()

    def _follow_pid(self) -> None:
        while not self.stop.wait(3):  # the app may restart; keep up with its pid
            self.pids = set(adb("shell", "pidof", APP_PACKAGE, check=False).split())

    def run(self) -> None:
        self.pids = set(adb("shell", "pidof", APP_PACKAGE, check=False).split())
        threading.Thread(target=self._follow_pid, daemon=True).start()
        self.proc = subprocess.Popen(["adb", "logcat", "-v", "threadtime", "-T", "1"],
                                     stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL, text=True, errors="replace")
        with open(self.path, "w") as out:
            for line in self.proc.stdout:
                out.write(line)
                fields = line.split(None, 6)  # date time pid tid level tag message
                if (len(fields) > 2 and fields[2] in self.pids and "PostHog" not in line
                        and (self.interesting is None or self.interesting.search(line))):
                    say("app", line.rstrip()[:200], line.rstrip() if len(line) > 201 else None)

    def close(self) -> None:
        self.stop.set()
        if self.proc:
            self.proc.terminate()


# --- WiFi monitor --------------------------------------------------------------------------

MAC_RE = re.compile(r"\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b")


class Monitor:
    """Puts the WiFi card in monitor mode, finds the recorder on the air, records everything.

    The recorder is recognised by its known AP MAC or by its WiFi OUI, so a recorder never seen
    before is found too, whether it raises an AP or joins a network as a client.
    """

    def __init__(self, iface: str, bssid: str | None, ouis: list[str], out: Path, hint: int | None):
        self.iface, self.out, self.hint = iface, out, hint
        self.known = {bssid.lower()} if bssid else set()
        self.ouis = tuple(o.lower() for o in ouis)
        self.seen: set[str] = set()  # the recorder's MACs seen so far
        self.ssids: set[tuple[str, str]] = set()  # (who, SSID) from probes and beacons
        self.restore_uuid: str | None = None
        self.capture: subprocess.Popen | None = None
        self.watch: subprocess.Popen | None = None
        self.locked: int | None = None
        self.eapol = 0
        self.frames = 0
        self.stop = threading.Event()
        self.in_monitor = False
        self._lock = threading.Lock()  # hopping vs. locking onto the recorder's channel

    def start(self) -> None:
        for line in run(["nmcli", "-t", "-f", "UUID,DEVICE", "connection", "show", "--active"]).splitlines():
            uuid, _, dev = line.rpartition(":")
            if dev == self.iface:
                self.restore_uuid = uuid
        self.in_monitor = True  # from here on, close() must restore the interface
        run(["nmcli", "device", "set", self.iface, "managed", "no"], sudo=True)
        run(["ip", "link", "set", self.iface, "down"], sudo=True)
        run(["iw", "dev", self.iface, "set", "type", "monitor"], sudo=True)
        run(["ip", "link", "set", self.iface, "up"], sudo=True)
        self._freq(self.hint or FREQS_24[0])
        say("wifi", f"{self.iface} is in monitor mode (laptop WiFi is off until the end)")
        user = getpass.getuser()
        self.capture = subprocess.Popen(
            ["sudo", "-n", "tcpdump", "-i", self.iface, "-Z", user, "-U", "-w", str(self.out)],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        # No filter: the recorder's MAC may be one we've never seen, so lines are matched here.
        self.watch = subprocess.Popen(
            ["sudo", "-n", "tcpdump", "-i", self.iface, "-l", "-n", "-e"],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True, errors="replace")
        threading.Thread(target=self._read_watch, daemon=True).start()
        threading.Thread(target=self._hop, daemon=True).start()

    def _freq(self, mhz: int) -> None:
        run(["iw", "dev", self.iface, "set", "freq", str(mhz)], sudo=True, check=False)

    def _hop_to(self, mhz: int) -> bool:
        """Tunes to mhz unless the recorder was found meanwhile; False once it's time to stop."""
        with self._lock:
            if self.locked is not None or self.stop.is_set():
                return False
            self._freq(mhz)
            return True

    def _hop(self) -> None:
        """Cycles the 2.4 GHz channels until the recorder shows up, lingering on the hint."""
        order = [f for f in FREQS_24 if f != self.hint]
        while True:
            if self.hint:
                if not self._hop_to(self.hint):
                    return
                self.stop.wait(1.5)
            for f in order:
                if not self._hop_to(f):
                    return
                self.stop.wait(0.3)

    def is_recorder(self, mac: str) -> bool:
        return mac in self.known or mac in self.seen or mac.startswith(self.ouis)

    def handle_line(self, line: str) -> None:
        """Acts on one line of `tcpdump -e` output."""
        low = line.lower()
        macs = {m for m in MAC_RE.findall(low) if self.is_recorder(m)}
        ssid_m = re.search(r"(Beacon|Probe Request|Probe Response) \(([^)]*)\)", line)
        kind, ssid = (ssid_m.group(1), ssid_m.group(2)) if ssid_m else (None, None)
        if not macs and not (ssid and ssid.startswith("PKT")):
            return
        self.frames += 1
        sa = re.search(r"\bsa:(\S+)", low)
        sender = sa.group(1) if sa else None
        freq = re.search(r"(\d{4}) MHz", line)
        mhz = freq.group(1) if freq else "?"
        for m in sorted(macs - self.seen):
            self.seen.add(m)
            say("wifi", f"recorder MAC {m} on {mhz} MHz ({kind or 'frame'})", line.strip())
        if kind and ssid is not None:
            who = "recorder" if sender and self.is_recorder(sender) else sender or "?"
            if (who, ssid) not in self.ssids and (ssid or kind != "Probe Request"):
                self.ssids.add((who, ssid))
                what = "looks for" if kind == "Probe Request" else "announces"
                say("wifi", f"{who} {what} network '{ssid or '<hidden>'}' on {mhz} MHz", line.strip())
        # A recorder sending probe requests is scanning every channel, so that's no reason to stay.
        if self.locked is None and freq and macs and kind != "Probe Request":
            with self._lock:
                self.locked = int(freq.group(1))
                self._freq(self.locked)
            say("wifi", f"recorder seen on {self.locked} MHz; staying there")
        if "EAPOL" in line and macs:
            self.eapol += 1
            say("wifi", f"WPA handshake frame {self.eapol} (need 4 from one join)")
            if self.eapol % 4 == 0:
                say("wifi", "a full handshake may be captured: the traffic can likely be decrypted")

    def _read_watch(self) -> None:
        for line in self.watch.stdout:
            self.handle_line(line)

    def close(self) -> None:
        self.stop.set()
        for p in (self.capture, self.watch):
            if p and p.poll() is None:
                p.terminate()
                try:
                    p.wait(5)
                except subprocess.TimeoutExpired:
                    p.kill()
        if not self.in_monitor:
            return
        run(["ip", "link", "set", self.iface, "down"], sudo=True, check=False)
        run(["iw", "dev", self.iface, "set", "type", "managed"], sudo=True, check=False)
        run(["ip", "link", "set", self.iface, "up"], sudo=True, check=False)
        run(["nmcli", "device", "set", self.iface, "managed", "yes"], sudo=True, check=False)
        if self.restore_uuid:
            time.sleep(3)
            run(["nmcli", "connection", "up", self.restore_uuid], check=False, timeout=60)
        say("wifi", f"{self.iface} is back in managed mode")


def channel_hint(bssid: str) -> int | None:
    """The recorder AP's frequency from earlier probe joins, as logged by wpa_supplicant."""
    for cmd in (["journalctl", "--since", "-7d", "-o", "cat", "-u", "wpa_supplicant"],
                ["journalctl", "--since", "-7d", "-o", "cat"]):
        try:
            text = run(cmd, check=False, timeout=60)
        except (OSError, subprocess.TimeoutExpired):
            continue
        hits = re.findall(rf"{re.escape(bssid)}.*?freq=(\d+)", text, re.I)
        if hits:
            return int(hits[-1])
    return None


# --- Phone ---------------------------------------------------------------------------------

def adb(*args: str, check: bool = True, timeout: float = 30) -> str:
    return run(["adb", *args], check=check, timeout=timeout)


def enable_snoop() -> None:
    mode = adb("shell", "getprop", "persist.bluetooth.btsnooplogmode", check=False).strip()
    if mode != "full":
        adb("shell", "setprop", "persist.bluetooth.btsnooplogmode", "full", check=False)
        mode = adb("shell", "getprop", "persist.bluetooth.btsnooplogmode", check=False).strip()
    if mode != "full":
        ask("On the phone: Settings → Developer options → Enable Bluetooth HCI snoop log → "
            "'Enabled' (not 'Filtered').")
        mode = adb("shell", "getprop", "persist.bluetooth.btsnooplogmode", check=False).strip()
    say("bt", f"snoop log mode: {mode or 'unknown (older Android: check Developer options)'}")
    restart_bluetooth()


def restart_bluetooth() -> None:
    """The snoop setting (and with it the snoop socket) only takes effect when Bluetooth restarts."""
    say("bt", "restarting Bluetooth on the phone so the snoop log starts")
    replies = ""
    for action in ("disable", "enable"):
        p = subprocess.run(["adb", "shell", "cmd", "bluetooth_manager", action],
                           capture_output=True, text=True, timeout=30, stdin=subprocess.DEVNULL)
        replies += p.stdout + p.stderr
        time.sleep(3)
    if re.search(r"unknown|not found|exception|error", replies, re.I):
        ask("Couldn't restart Bluetooth from adb. Turn Bluetooth off and on again on the phone.")


def phone_listening(port: int) -> bool | None:
    """Whether something on the phone listens on TCP `port`; None if the phone won't say.

    `adb forward` accepts the laptop's connection even when nothing listens on the phone (it
    just closes it right away), so this asks the phone itself.
    """
    for cmd in (["ss", "-ltn"], ["netstat", "-ltn"]):
        out = adb("shell", *cmd, check=False)
        if re.search(r"State|Proto", out):  # the tool ran and printed its table
            return bool(re.search(rf"[:.]{port}\s", out))
    out = adb("shell", "cat", "/proc/net/tcp", "/proc/net/tcp6", check=False)
    rows = [l.split() for l in out.splitlines() if re.match(r"\s*\d+:", l)]
    if not rows:
        return None
    # local_address is "<hex ip>:<hex port>", state 0A = LISTEN
    return any(r[1].endswith(f":{port:04X}") and r[3] == "0A" for r in rows if len(r) > 3)


def snoop_props() -> str:
    """The phone's snoop-related Bluetooth properties, for diagnosis."""
    out = adb("shell", "getprop", check=False)
    return ", ".join(l.strip() for l in out.splitlines() if "snoop" in l.lower()) or "none set"


def open_snoop(local_port: int) -> tuple[socket.socket | None, bytes, int]:
    """Forwards and connects to the snoop socket, helping the user until it works or they give up.

    Returns (socket, header, laptop port); the socket is None without a live stream.
    """
    port = forward_snoop(local_port)
    say("bt", f"adb forward: laptop port {port} -> phone port {SNOOP_PORT}")
    while True:
        sock, header, why = connect_snoop(port)
        if sock:
            say("bt", f"snoop socket answers with a btsnoop header (datalink "
                      f"{struct.unpack('>I', header[12:16])[0]})")
            return sock, header, port
        listening = phone_listening(SNOOP_PORT)
        say("bt", f"no live snoop stream: {why}")
        say("bt", f"phone says port {SNOOP_PORT} is "
                  f"{ {True: 'listening', False: 'not listening', None: 'unknown (no ss/netstat access)'}[listening]}; "
                  f"properties: {snoop_props()}")
        with _print_lock:
            print("\n>>> The usual causes: the snoop log is 'Filtered' or off, Bluetooth wasn't restarted\n"
                  "    since enabling it, another client (Wireshark/androiddump, a second adb forward)\n"
                  "    holds the socket, or this Android build has no snoop socket (common on release\n"
                  "    builds; the bug report at the end still has the log).", flush=True)
        choice = prompt(">>> [r] restart Bluetooth and check again, [c] continue without the live log: ")
        if choice.strip().lower().startswith("c"):
            return None, b"", port
        restart_bluetooth()


def save_phone_state(outdir: Path, label: str) -> None:
    """The phone's Bluetooth state (bonded devices, GATT clients), to compare before and after."""
    out = adb("shell", "dumpsys", "bluetooth_manager", check=False, timeout=60)
    (outdir / f"phone-bluetooth-{label}.txt").write_text(out)
    say("phone", f"saved the phone's Bluetooth state ({label})", quiet=True)


def pull_bugreport(outdir: Path) -> None:
    say("bt", "taking a bug report for the on-phone snoop log (takes 1–3 minutes)")
    zpath = outdir / "bugreport.zip"
    p = subprocess.run(["adb", "bugreport", str(zpath)], capture_output=True, text=True, timeout=600,
                       stdin=subprocess.DEVNULL)
    if not zpath.exists():
        say("bt", f"bug report failed: {(p.stderr or p.stdout).strip()[-200:]}")
        return
    with zipfile.ZipFile(zpath) as z:
        logs = [n for n in z.namelist() if "btsnoop_hci.log" in n]
        for name in logs:
            target = outdir / ("phone-" + name.replace("/", "_"))
            target.write_bytes(z.read(name))
            say("bt", f"saved {target.name} ({target.stat().st_size} bytes)")
    if not logs:
        say("bt", "the bug report has no btsnoop_hci.log (is the snoop log enabled?)")


def decode_saved(outdir: Path) -> None:
    """Writes <log>.decoded.txt next to every btsnoop log in outdir, with every detail."""
    for f in sorted(outdir.iterdir()):
        if not f.is_file() or f.suffix == ".txt":
            continue
        with open(f, "rb") as fh:
            if fh.read(8) != b"btsnoop\0":
                continue
        target = f.with_name(f.name + ".decoded.txt")
        with open(target, "w") as out:
            try:
                n = decode_file(f, out, everything=True)
            except ValueError as e:
                say("bt", f"couldn't decode {f.name}: {e}")
                continue
        say("bt", f"decoded {f.name}: {n} packets -> {target.name}")


# --- Scenarios -----------------------------------------------------------------------------

@dataclass
class Scenario:
    help: str
    ready: str
    now: str
    log_filter: re.Pattern | None  # app log lines to show; None shows all of them


SCENARIOS = {
    "activation": Scenario(
        help="the app activating (setting up) a recorder",
        ready="Get ready, but DON'T start the activation yet:\n"
              "    - The recorder is in the state it's in before activation (new, or however the app\n"
              "      lets you activate it again), charged, near the phone and the laptop.\n"
              "    - To capture the Bluetooth pairing too, remove the recorder from the phone's\n"
              "      Bluetooth settings (Forget) if it's listed there.\n"
              "    - Nothing else connected to the recorder: no pocket-wifi-probe, no desktop app.\n"
              "    - Phone: no VPN, location on, the Pocket app open.",
        now="NOW, in the Pocket app, activate the recorder:\n"
            "    1. Start setting it up and follow every step until the app says it's done (or fails).\n"
            "    2. At each step, type a short note here ('tapped Add device', 'entered home WiFi',\n"
            "       'firmware update started', what the app shows). It lands in the timeline next to\n"
            "       the packets.\n"
            "    3. 'phone>'/'device<' lines are Bluetooth traffic, 'gatt' the recorder's services,\n"
            "       'adv' its advertising, 'wifi' the recorder on WiFi (an AP, or joining a network).",
        log_filter=None,
    ),
    "quick-transfer": Scenario(
        help="the app's Quick Transfer (Wi-Fi)",
        ready="Get ready, but DON'T start a transfer yet:\n"
              "    - Recorder on, battery above 10 %, near the phone and the laptop.\n"
              "    - The recorder has at least one recording the app hasn't imported yet. If not, record\n"
              "      a minute or two now, so the transfer takes a while.\n"
              "    - Nothing else connected to the recorder: no pocket-wifi-probe, no desktop app.\n"
              "    - Phone: no VPN, location on, the Pocket app open.",
        now="NOW, in the Pocket app:\n"
            "    1. Make sure it shows the recorder as connected (it may reconnect after the\n"
            "       Bluetooth restart; give it a few seconds).\n"
            "    2. Open the sheet with the recordings not imported yet, select one or more, and tap\n"
            "       'Quick Transfer (Wi-Fi)'. Accept any prompt to join the Pocket's Wi-Fi.\n"
            "    3. Wait until the app says the transfer finished or failed. Watch the lines below:\n"
            "       'phone>'/'device<' are Bluetooth commands, 'wifi' the AP and the handshake.\n"
            "    4. If no handshake line showed up, run a second Quick Transfer of another\n"
            "       recording, so the phone joins again while the capture is on the channel.\n"
            "    Note anything the app shows (errors, firmware-update prompts).",
        log_filter=re.compile(r"wi-?fi|transfer|switch|frame|socket|prewarm|handoff|APP&|MCU&|8475", re.I),
    ),
}


# --- Main ----------------------------------------------------------------------------------

def preflight(args) -> str | None:
    tools = ["adb"] + ([] if args.no_wifi else ["iw", "ip", "tcpdump", "nmcli", "sudo"])
    missing = [t for t in tools if not shutil.which(t)]
    if missing:
        sys.exit(f"Missing tools: {', '.join(missing)}")
    devices = [l for l in adb("devices").splitlines()[1:] if l.endswith("\tdevice")]
    if len(devices) != 1:
        sys.exit(f"Need exactly one phone on adb (USB debugging on, authorised); found {len(devices)}.")
    model = adb("shell", "getprop", "ro.product.model").strip()
    release = adb("shell", "getprop", "ro.build.version.release").strip()
    say("phone", f"{model}, Android {release}")
    if not adb("shell", "pm", "path", APP_PACKAGE, check=False).strip():
        sys.exit(f"The Pocket app ({APP_PACKAGE}) isn't installed on this phone.")
    if args.no_wifi:
        return None
    iface = args.iface or next(
        (l.split(":")[0] for l in run(["nmcli", "-t", "-f", "DEVICE,TYPE", "device"]).splitlines()
         if l.split(":")[1:2] == ["wifi"]), None)
    if not iface:
        sys.exit("No WiFi interface found; pass --iface.")
    say("laptop", f"WiFi interface {iface}")
    print("\nsudo is needed for monitor mode and tcpdump.")
    subprocess.run(["sudo", "-v"], check=True)
    return iface


def keep_sudo(stop: threading.Event) -> None:
    while not stop.wait(60):
        subprocess.run(["sudo", "-n", "-v"], capture_output=True)


def capture(args) -> int:
    global _timeline
    scenario = SCENARIOS[args.scenario]
    outdir = Path(f"capture-{args.scenario}-{datetime.now():%Y%m%d-%H%M%S}")
    step("Checking the laptop and the phone")
    iface = preflight(args)
    outdir.mkdir()
    _timeline = open(outdir / "timeline.log", "w")
    say("laptop", f"capturing {scenario.help}; saving everything to {outdir}/")

    ask(scenario.ready)

    stop_sudo = threading.Event()
    if iface:
        threading.Thread(target=keep_sudo, args=(stop_sudo,), daemon=True).start()
    snoop = logcat = monitor = None
    snoop_port = None
    decoder = HciDecoder(say, args.device_name, tuple(args.device_addr))
    try:
        step("Bluetooth snoop log on the phone")
        save_phone_state(outdir, "before")
        enable_snoop()
        sock, header, snoop_port = open_snoop(args.snoop_port)
        if sock:
            snoop = SnoopStream(outdir / "phone-live.btsnoop", sock, header, decoder)
            snoop.start()
            if not snoop.first.wait(15):
                say("bt", "the header came, but no Bluetooth packets within 15 s. Open the Pocket app "
                          "so the phone talks Bluetooth; if nothing shows up, the bug report at the "
                          "end still has the log.")
        else:
            say("bt", "no live Bluetooth log; the bug report at the end has it")

        step("App log")
        logcat = Logcat(outdir / "logcat.txt", scenario.log_filter)
        logcat.start()
        say("app", "recording adb logcat")

        if iface:
            step("WiFi capture")
            hint = args.freq or (channel_hint(args.bssid) if args.bssid else None)
            say("wifi", f"AP frequency from earlier joins: {hint} MHz" if hint and not args.freq
                else f"AP frequency: {hint} MHz" if hint else "recorder's channel unknown; scanning 2.4 GHz")
            monitor = Monitor(iface, args.bssid, args.oui, outdir / "wifi-monitor.pcap", hint)
            monitor.start()

        mark_until_done(scenario.now)
    except KeyboardInterrupt:
        say("laptop", "interrupted")
    finally:
        step("Cleaning up")
        if monitor:
            monitor.close()
        if logcat:
            logcat.close()
        if snoop:
            snoop.stop.set()
        if snoop_port:
            adb("forward", "--remove", f"tcp:{snoop_port}", check=False)
        stop_sudo.set()
        restore_tty()

    save_phone_state(outdir, "after")
    if not args.no_bugreport:
        pull_bugreport(outdir)
    if snoop:
        snoop.join(3)
    decode_saved(outdir)

    step("Summary")
    if snoop:
        say("bt", f"live snoop: {decoder.packets} packets, {snoop.bytes} bytes")
    if decoder.seen:
        say("bt", "recorders seen: " + ", ".join(decoder._who(a) for a in sorted(decoder.seen)))
    if monitor:
        say("wifi", f"recorder {'on ' + str(monitor.locked) + ' MHz' if monitor.locked else 'never seen'}, "
                    f"{monitor.frames} frames, {monitor.eapol} handshake frames, "
                    f"MACs: {', '.join(sorted(monitor.seen)) or 'none'}")
        for who, ssid in sorted(monitor.ssids):
            say("wifi", f"network: {who} -> '{ssid or '<hidden>'}'")
    for f in sorted(outdir.iterdir()):
        say("file", f"{f} ({f.stat().st_size} bytes)")
    print("\nTo decrypt wifi-monitor.pcap in Wireshark: IEEE 802.11 decryption key, wpa-pwd "
          "<password>:<SSID>. On the recorder's AP the password is the first 8 characters of the "
          "session key, and the SSID is in the MCU&WIFI& reply (see timeline.log).")
    print(f"\nTell Claude: capture is in {outdir}/ (plus what the app showed). The files contain the "
          "session key and WiFi passwords, so keep them private.")
    timeline, _timeline = _timeline, None
    timeline.close()
    return 0


def snoop_check(args) -> int:
    """Checks the live Bluetooth log end to end, without capturing anything else."""
    args.no_wifi = True
    step("Checking the phone")
    preflight(args)
    say("bt", f"snoop log mode: "
              f"{adb('shell', 'getprop', 'persist.bluetooth.btsnooplogmode', check=False).strip() or 'not set'}")
    say("bt", f"snoop properties: {snoop_props()}")
    say("bt", f"phone says port {SNOOP_PORT} is "
              f"{ {True: 'listening', False: 'not listening', None: 'unknown'}[phone_listening(SNOOP_PORT)]}")
    if args.restart:
        restart_bluetooth()
    port = forward_snoop(args.snoop_port)
    say("bt", f"adb forward: laptop port {port} -> phone port {SNOOP_PORT}")
    try:
        sock, header, why = connect_snoop(port)
        if not sock:
            say("bt", f"FAILED: {why}")
            print("\nTry --restart (the setting only takes effect when Bluetooth restarts), close "
                  "Wireshark/androiddump, and check 'adb forward --list' for other forwards to 8872.")
            return 1
        say("bt", f"header OK (datalink {struct.unpack('>I', header[12:16])[0]}); showing packets for "
                  f"{args.seconds:g} s. Open the Pocket app or toggle something Bluetooth on the phone.")
        decoder = HciDecoder(say)
        with tempfile.TemporaryDirectory() as tmp:
            stream = SnoopStream(Path(tmp) / "check.btsnoop", sock, header, decoder)
            stream.start()
            time.sleep(args.seconds)
            stream.stop.set()
            stream.join(3)
        if not decoder.packets:
            say("bt", "the header came, but no packets: the socket works, but there was no Bluetooth "
                      "traffic (or the log is 'Filtered'). Try again with the Pocket app open.")
            return 1
        say("bt", f"OK: {decoder.packets} packets in {args.seconds:g} s through the forward")
        return 0
    finally:
        adb("forward", "--remove", f"tcp:{port}", check=False)


def decode(args) -> int:
    for path in args.file:
        try:
            decode_file(Path(path), sys.stdout, everything=args.all)
        except (OSError, ValueError) as e:
            print(f"{path}: {e}", file=sys.stderr)
            return 1
    return 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = p.add_subparsers(dest="scenario", required=True,
                           metavar="{activation,quick-transfer,snoop-check,decode}")
    for name, sc in SCENARIOS.items():
        s = sub.add_parser(name, help=f"capture {sc.help}")
        s.add_argument("--iface", help="WiFi interface to put in monitor mode (default: the first one)")
        s.add_argument("--bssid", default=RECORDER_BSSID,
                       help=f"the recorder AP's MAC, if known (default {RECORDER_BSSID}); '' for none")
        s.add_argument("--oui", action="append", default=[RECORDER_WIFI_OUI],
                       help=f"WiFi MAC prefix that marks a recorder (default {RECORDER_WIFI_OUI}; repeatable)")
        s.add_argument("--freq", type=int, help="the recorder's WiFi frequency in MHz, if known (default: "
                                                "from the system log of earlier joins, else scan 2.4 GHz)")
        s.add_argument("--device-name", default=RECORDER_NAME,
                       help=f"regex for the recorder's Bluetooth name (default {RECORDER_NAME!r})")
        s.add_argument("--device-addr", action="append", default=[RECORDER_BT_ADDR],
                       help=f"Bluetooth address of a recorder (default {RECORDER_BT_ADDR}; repeatable)")
        s.add_argument("--no-wifi", action="store_true", help="only capture Bluetooth and the app log")
        s.add_argument("--no-bugreport", action="store_true", help="skip the bug report at the end")
        s.add_argument("--snoop-port", type=int, default=0,
                       help="laptop port for the adb forward to the snoop socket (default: a free one)")
        s.set_defaults(func=capture)
    c = sub.add_parser("snoop-check", help="check that the phone's snoop socket streams through adb forward")
    c.add_argument("--restart", action="store_true", help="restart the phone's Bluetooth first")
    c.add_argument("--seconds", type=float, default=20, help="how long to show packets (default 20)")
    c.add_argument("--snoop-port", type=int, default=0,
                   help="laptop port for the adb forward (default: a free one)")
    c.set_defaults(func=snoop_check)
    d = sub.add_parser("decode", help="decode a saved btsnoop file (no phone needed)")
    d.add_argument("file", nargs="+")
    d.add_argument("--all", action="store_true",
                   help="also show discovery details, scanning, every advertising change and full values")
    d.set_defaults(func=decode)
    args = p.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
