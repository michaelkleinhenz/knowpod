#!/usr/bin/env python3
"""Captures the official Pocket app doing a Quick Transfer, to work out the WiFi protocol.

Runs on a Linux laptop with an Android phone attached over adb (USB debugging on). Captures,
all at once:

- Bluetooth: the phone's HCI snoop log, streamed live from the phone's btsnoop socket (TCP 8872)
  through `adb forward`. It's decoded as it arrives, so the app's APP&/MCU& commands print as
  they happen. A bug report at the end also pulls the log file kept on the phone, in case the
  socket isn't there (release builds of Android often leave it out).
- App log: `adb logcat`, for the app's own messages ("Switching file transfer to Wi-Fi.", …).
- WiFi: this laptop's card in monitor mode, recording the phone <-> recorder traffic on the
  recorder's access point. It's encrypted (WPA2-PSK); decrypting needs the phone's 4-way
  handshake, so the capture must be running before the phone joins.

Everything goes into qt-capture-<time>/. The laptop's WiFi is put back afterwards, also after
Ctrl-C. Standard library only; run it with the system python3, not inside a sandbox.

    python3 capture_quick_transfer.py
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
import termios
import threading
import time
import zipfile
from datetime import datetime
from pathlib import Path

RECORDER_BSSID = "a4:c1:38:81:90:50"
RECORDER_SSID = "PKT01_GREY_261717ff"
APP_PACKAGE = "com.heypocket.app"
SNOOP_PORT = 8872
FREQS_24 = [2412 + 5 * i for i in range(13)]

T0 = time.monotonic()
_print_lock = threading.Lock()
# adb puts a terminal it can reach into raw mode and reads the keys itself, so every child gets
# stdin=DEVNULL, and the terminal settings from startup are put back before each prompt.
_TTY = termios.tcgetattr(sys.stdin) if sys.stdin.isatty() else None


def restore_tty() -> None:
    if _TTY is not None:
        termios.tcsetattr(sys.stdin, termios.TCSANOW, _TTY)


def prompt(text: str) -> str:
    restore_tty()
    return input(text)


def say(kind: str, text: str) -> None:
    with _print_lock:
        print(f"[{time.monotonic() - T0:7.1f}] {kind:<8} {text}", flush=True)


def step(text: str) -> None:
    with _print_lock:
        print(f"\n=== {text}", flush=True)


def ask(text: str) -> None:
    """Tells the user what to do now and waits for Enter."""
    with _print_lock:
        print(f"\n>>> {text}", flush=True)
    prompt(">>> Press Enter when done. ")


def run(cmd: list[str], *, sudo: bool = False, check: bool = True, timeout: float = 30) -> str:
    if sudo:
        cmd = ["sudo", "-n", *cmd]
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, stdin=subprocess.DEVNULL)
    if check and p.returncode != 0:
        raise RuntimeError(f"{' '.join(cmd)} failed: {(p.stderr or p.stdout).strip()}")
    return p.stdout


def redact(text: str) -> str:
    text = re.sub(r"(APP&SK&)\S+", r"\1<redacted>", text)
    return re.sub(r"(MCU&WIFI&[^&]+&)\S+", r"\1<redacted>", text)


# --- Bluetooth: btsnoop stream -------------------------------------------------------------

class SnoopStream(threading.Thread):
    """Reads the phone's btsnoop socket, saves it as a .btsnoop file and prints ATT commands."""

    def __init__(self, path: Path, port: int):
        super().__init__(daemon=True)
        self.path, self.port = path, port
        self.bytes = 0
        self.records = 0
        self.binary_notifications = 0
        self.stop = threading.Event()
        self.ok = threading.Event()  # set once a valid btsnoop header arrived

    def run(self) -> None:
        try:
            sock = socket.create_connection(("127.0.0.1", self.port), timeout=5)
        except OSError as e:
            say("bt", f"no snoop socket ({e}); will use the bug report instead")
            return
        sock.settimeout(1)
        buf = b""
        with sock, open(self.path, "wb") as out:
            while not self.stop.is_set():
                try:
                    data = sock.recv(65536)
                except socket.timeout:
                    continue
                except OSError:
                    break
                if not data:
                    break
                out.write(data)
                out.flush()
                self.bytes += len(data)
                buf += data
                if not self.ok.is_set():
                    if len(buf) < 16:
                        continue
                    if not buf.startswith(b"btsnoop\0"):
                        say("bt", f"snoop socket sent no btsnoop header ({buf[:16].hex()})")
                        return
                    self.ok.set()
                    say("bt", "snoop socket is streaming")
                    buf = buf[16:]
                buf = self._records(buf)
        if self.ok.is_set():
            say("bt", f"snoop stream closed after {self.records} packets")
        else:
            say("bt", "snoop socket closed without data; will use the bug report instead")

    def _records(self, buf: bytes) -> bytes:
        while len(buf) >= 24:
            _orig, incl, _flags, _drops, _ts = struct.unpack(">IIIIq", buf[:24])
            if len(buf) < 24 + incl:
                break
            self.records += 1
            self._packet(buf[24:24 + incl])
            buf = buf[24 + incl:]
        return buf

    def _packet(self, pkt: bytes) -> None:
        # H4 type 0x02 = ACL; then handle/flags(2) len(2), L2CAP len(2) cid(2), ATT.
        if len(pkt) < 10 or pkt[0] != 0x02:
            return
        if (pkt[2] >> 4) & 0x3 == 0x1:  # continuation fragment: no L2CAP header
            return
        if struct.unpack("<H", pkt[7:9])[0] != 0x0004:  # ATT channel
            return
        att = pkt[9:]
        if not att:
            return
        op = att[0]
        if op in (0x12, 0x52) and len(att) >= 3:  # write request / command
            handle, value = struct.unpack("<H", att[1:3])[0], att[3:]
            if value.startswith(b"APP&"):
                say("phone>", f"{redact(value.decode('ascii', 'replace'))}   (handle 0x{handle:04x})")
            else:
                say("phone>", f"write 0x{handle:04x}: {value[:32].hex()}")
        elif op in (0x1B, 0x1D) and len(att) >= 3:  # notification / indication
            handle, value = struct.unpack("<H", att[1:3])[0], att[3:]
            if value.startswith(b"MCU&"):
                say("pocket<", f"{redact(value.decode('ascii', 'replace'))}   (handle 0x{handle:04x})")
            else:
                self.binary_notifications += 1
                n = self.binary_notifications
                if n == 1 or n % 500 == 0:
                    say("pocket<", f"{n} binary notifications so far (handle 0x{handle:04x}, "
                                   f"{len(value)} bytes, {value[:8].hex()}…)")


# --- App log -------------------------------------------------------------------------------

LOG_INTERESTING = re.compile(r"wi-?fi|transfer|switch|frame|socket|prewarm|handoff|APP&|MCU&|8475", re.I)


class Logcat(threading.Thread):
    """Saves all of adb logcat, and prints the relevant lines from the app's own process."""

    def __init__(self, path: Path):
        super().__init__(daemon=True)
        self.path = path
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
                        and LOG_INTERESTING.search(line)):
                    say("app", redact(line.rstrip())[:200])

    def close(self) -> None:
        self.stop.set()
        if self.proc:
            self.proc.terminate()


# --- WiFi monitor --------------------------------------------------------------------------

class Monitor:
    """Puts the WiFi card in monitor mode, finds the recorder's AP, records everything."""

    def __init__(self, iface: str, bssid: str, out: Path, hint: int | None):
        self.iface, self.bssid, self.out, self.hint = iface, bssid.lower(), out, hint
        self.restore_uuid: str | None = None
        self.capture: subprocess.Popen | None = None
        self.watch: subprocess.Popen | None = None
        self.locked: int | None = None
        self.eapol = 0
        self.frames = 0
        self.stop = threading.Event()
        self.in_monitor = False
        self._lock = threading.Lock()  # hopping vs. locking onto the AP's channel

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
        b = self.bssid
        self.watch = subprocess.Popen(
            ["sudo", "-n", "tcpdump", "-i", self.iface, "-l", "-n", "-e",
             f"wlan addr1 {b} or wlan addr2 {b} or wlan addr3 {b}"],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True, errors="replace")
        threading.Thread(target=self._read_watch, daemon=True).start()
        threading.Thread(target=self._hop, daemon=True).start()

    def _freq(self, mhz: int) -> None:
        run(["iw", "dev", self.iface, "set", "freq", str(mhz)], sudo=True, check=False)

    def _hop_to(self, mhz: int) -> bool:
        """Tunes to mhz unless the AP was found meanwhile; False once it's time to stop."""
        with self._lock:
            if self.locked is not None or self.stop.is_set():
                return False
            self._freq(mhz)
            return True

    def _hop(self) -> None:
        """Cycles the 2.4 GHz channels until the recorder's AP shows up, lingering on the hint."""
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

    def _read_watch(self) -> None:
        for line in self.watch.stdout:
            self.frames += 1
            m = re.search(r"(\d{4}) MHz", line) if self.locked is None else None
            if m:
                with self._lock:
                    self.locked = int(m.group(1))
                    self._freq(self.locked)
                say("wifi", f"recorder's AP seen on {self.locked} MHz; staying there")
            if "EAPOL" in line:
                self.eapol += 1
                say("wifi", f"WPA handshake frame {self.eapol} (need 4 from one join)")
                if self.eapol == 4:
                    say("wifi", "handshake captured: the traffic can be decrypted")

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


def wait_for_snoop_port(timeout: float = 15) -> bool:
    """Waits until the phone listens on the snoop port. False means: no live stream."""
    while True:
        deadline = time.monotonic() + timeout
        state = phone_listening(SNOOP_PORT)
        while state is False and time.monotonic() < deadline:
            time.sleep(1)
            state = phone_listening(SNOOP_PORT)
        if state:
            say("bt", f"the phone is listening on {SNOOP_PORT}")
            return True
        if state is None:
            say("bt", f"can't see the phone's open ports (no ss/netstat/proc access); trying {SNOOP_PORT} anyway")
            return True
        say("bt", f"the phone is NOT listening on {SNOOP_PORT} after {timeout:g}s")
        with _print_lock:
            print(f"\n>>> The snoop socket isn't open. Its usual causes: the snoop log is 'Filtered' or off,\n"
                  "    Bluetooth wasn't restarted since enabling it, or this Android build has no snoop\n"
                  "    socket (common on release builds; the bug report at the end still has the log).", flush=True)
        choice = prompt(">>> [r] restart Bluetooth and check again, [c] continue without the live log: ")
        if choice.strip().lower().startswith("c"):
            return False
        restart_bluetooth()


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


# --- Main ----------------------------------------------------------------------------------

def preflight(args) -> str:
    missing = [t for t in ("adb", "iw", "ip", "tcpdump", "nmcli", "sudo") if not shutil.which(t)]
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


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--iface", help="WiFi interface to put in monitor mode (default: the first one)")
    p.add_argument("--bssid", default=RECORDER_BSSID, help=f"the recorder AP's MAC (default {RECORDER_BSSID})")
    p.add_argument("--freq", type=int, help="the AP's frequency in MHz, if known (default: from the "
                                            "system log of earlier joins, else scan 2.4 GHz)")
    p.add_argument("--no-wifi", action="store_true", help="only capture Bluetooth and the app log")
    p.add_argument("--no-bugreport", action="store_true", help="skip the bug report at the end")
    p.add_argument("--snoop-port", type=int, default=SNOOP_PORT, help=f"local port for adb forward (default {SNOOP_PORT})")
    args = p.parse_args()

    outdir = Path(f"qt-capture-{datetime.now():%Y%m%d-%H%M%S}")
    step("Checking the laptop and the phone")
    iface = preflight(args)
    outdir.mkdir()
    say("laptop", f"saving everything to {outdir}/")

    ask("Get ready, but DON'T start a transfer yet:\n"
        "    - Recorder on, battery above 10 %, near the phone and the laptop.\n"
        f"    - The recorder has at least one recording the app hasn't imported yet. If not, record\n"
        "      a minute or two now, so the transfer takes a while.\n"
        "    - Nothing else connected to the recorder: no pocket-wifi-probe, no desktop app.\n"
        "    - Phone: no VPN, location on, the Pocket app open.")

    stop_sudo = threading.Event()
    threading.Thread(target=keep_sudo, args=(stop_sudo,), daemon=True).start()
    snoop = logcat = monitor = None
    try:
        step("Bluetooth snoop log on the phone")
        enable_snoop()
        if wait_for_snoop_port():
            adb("forward", f"tcp:{args.snoop_port}", f"tcp:{SNOOP_PORT}")
            snoop = SnoopStream(outdir / "phone-live.btsnoop", args.snoop_port)
            snoop.start()
            if not snoop.ok.wait(6):
                say("bt", "connected, but no btsnoop header within 6 s. The socket takes one client at a "
                          "time: close Wireshark/androiddump or other adb forwards to it. The bug report "
                          "at the end still has the log.")
        else:
            say("bt", "no live Bluetooth log; the bug report at the end has it")

        step("App log")
        logcat = Logcat(outdir / "logcat.txt")
        logcat.start()
        say("app", "recording adb logcat")

        if not args.no_wifi:
            step("WiFi capture")
            hint = args.freq or channel_hint(args.bssid)
            say("wifi", f"AP frequency from earlier joins: {hint} MHz" if hint and not args.freq
                else f"AP frequency: {hint} MHz" if hint else "AP frequency unknown; scanning 2.4 GHz")
            monitor = Monitor(iface, args.bssid, outdir / "wifi-monitor.pcap", hint)
            monitor.start()

        ask("NOW, in the Pocket app:\n"
            "    1. Make sure it shows the recorder as connected (it may reconnect after the\n"
            "       Bluetooth restart; give it a few seconds).\n"
            "    2. Open the sheet with the recordings not imported yet, select one or more, and tap\n"
            "       'Quick Transfer (Wi-Fi)'. Accept any prompt to join the Pocket's Wi-Fi.\n"
            "    3. Wait until the app says the transfer finished or failed. Watch the lines below:\n"
            "       'phone>'/'pocket<' are Bluetooth commands, 'wifi' the AP and the handshake.\n"
            "    4. If no 'handshake captured' line showed up, run a second Quick Transfer of\n"
            "       another recording, so the phone joins again while the capture is on the channel.\n"
            "    Note anything the app shows (errors, firmware-update prompts).")
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
        adb("forward", "--remove", f"tcp:{args.snoop_port}", check=False)
        stop_sudo.set()
        restore_tty()

    if not args.no_bugreport:
        pull_bugreport(outdir)

    step("Summary")
    if snoop:
        say("bt", f"live snoop: {snoop.records} packets, {snoop.bytes} bytes, "
                  f"{snoop.binary_notifications} binary notifications")
    if monitor:
        say("wifi", f"AP {'on ' + str(monitor.locked) + ' MHz' if monitor.locked else 'never seen'}, "
                    f"{monitor.frames} frames to/from it, {monitor.eapol} handshake frames")
    for f in sorted(outdir.iterdir()):
        say("file", f"{f} ({f.stat().st_size} bytes)")
    print(f"\nTo decrypt wifi-monitor.pcap in Wireshark: IEEE 802.11 decryption key, wpa-pwd "
          f"<first 8 characters of the session key>:{RECORDER_SSID}")
    print(f"\nTell Claude: capture is in {outdir}/ (plus what the app showed). The files contain the "
          "session key and WiFi password, so keep them private.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
