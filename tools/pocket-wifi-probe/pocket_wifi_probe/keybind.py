"""pocket-keybind: find out how a Pocket recorder accepts its session key, over this machine's
Bluetooth. For a recorder you own; useful right after a factory reset.

Background (see RESEARCH.md): the official app's whole activation of a reset recorder, on the
Bluetooth side, was a single `APP&SK&<key>` answered `MCU&SK&OK`, where the key is the first 16
characters of the account's user ID. The open question is how the recorder decides which key to
accept: does it adopt the first key it sees and then keep it (first-key-wins), or does it accept
any key, or something else?

This answers that by running a few connections in turn and reporting what the recorder does. Each
step opens its own BLE connection, optionally sends one `APP&SK&<key>`, then sends `APP&BAT` and
notes whether the recorder answers. `MCU&SK&OK` alone isn't proof of being unlocked, so the
`APP&BAT` reply is the real signal, and it also shows whether the recorder dropped the link.

Steps (comma-separated, `--steps`):

    none            no key at all: does the recorder answer commands unlocked?
    key             the key passed on the command line / POCKET_SESSION_KEY (on a reset
                    recorder, this is the activation)
    fresh           a freshly generated 16-char key (NOT the command-line one)
    other:<KEY>     a specific key, e.g. the one the vendor app used before the reset

The default run is `none,key,fresh,key`: unlocked access first, then the key, then a different
key, then the first key again. On a first-key-wins recorder you would see: none refused, key OK,
fresh refused, key OK again.
"""
from __future__ import annotations

import argparse
import asyncio
import datetime as dt
import json
import logging
import os
import re
import secrets
import string
import time
import traceback

from .pocket import PocketError, PocketLink

KEY_CHARS = string.ascii_letters + string.digits
T0 = time.monotonic()
_logfile = None  # every line printed, plus bleak's debug log and tracebacks


def log(kind: str, text: str) -> None:
    line = f"[{time.monotonic() - T0:6.1f}] {kind:<7} {text}"
    print(line, flush=True)
    if _logfile:
        _logfile.write(line + "\n")
        _logfile.flush()


def log_traceback() -> None:
    if _logfile:
        _logfile.write(traceback.format_exc())
        _logfile.flush()


def open_log(path: str) -> None:
    """Sends our lines and bleak's debug log (the BlueZ/D-Bus calls) to `path`."""
    global _logfile
    _logfile = open(path, "w")
    handler = logging.StreamHandler(_logfile)
    handler.setFormatter(logging.Formatter("%(relativeCreated)9.0fms %(name)s: %(message)s"))
    bleak_log = logging.getLogger("bleak")
    bleak_log.setLevel(logging.DEBUG)
    bleak_log.addHandler(handler)
    bleak_log.propagate = False  # keep the debug lines off the screen


def fresh_key() -> str:
    """A 16-character key shaped like the app's (a Firebase user ID's first 16 characters)."""
    return "".join(secrets.choice(KEY_CHARS) for _ in range(16))


def parse_steps(text: str) -> list[tuple[str, str | None]]:
    steps: list[tuple[str, str | None]] = []
    for part in (p.strip() for p in text.split(",") if p.strip()):
        kind, _, value = part.partition(":")
        if kind in ("none", "key", "fresh") and not value:
            steps.append((kind, None))
        elif kind == "other" and value:
            steps.append((kind, value))
        else:
            raise argparse.ArgumentTypeError(
                f"bad step {part!r}: use none, key, fresh, or other:<KEY>")
    if not steps:
        raise argparse.ArgumentTypeError("no steps given")
    return steps


def redactor(secrets_: list[str]):
    secrets_ = [s for s in secrets_ if s]

    def redact(text: str) -> str:
        for s in secrets_:
            text = text.replace(s, "<key>")
        return text

    return redact


async def open_link(address: str, *, scan_timeout: float, attempts: int, redact,
                    app_style: bool = True):
    """Opens a PocketLink, retrying the scan+connect: after unlocking, the recorder raises its
    WiFi AP and stops advertising over BLE for ~15 s, so the next step often can't find it on
    the first try. Returns an entered link, or raises the last error."""
    last: Exception | None = None
    for i in range(1, attempts + 1):
        link = PocketLink(address, scan_timeout=scan_timeout, direct_fallback=True,
                          app_style=app_style, force_le=True,
                          on_event=lambda kind, text: log(kind, redact(text)))
        try:
            await link.__aenter__()
            return link
        except (KeyboardInterrupt, asyncio.CancelledError):
            raise
        except Exception as e:  # noqa: BLE001 - not-found, connect timeout, BlueZ errors: all retry
            last = e
            log_traceback()
            try:
                await link.close()
            except Exception:  # noqa: BLE001
                pass
            kind = type(e).__name__
            detail = redact(str(e)) or "no detail"
            if isinstance(e, (asyncio.TimeoutError, TimeoutError)):
                detail = ("scan/connect timed out: the recorder isn't reachable. Most often the "
                          "phone still holds the Bluetooth link, or the recorder is asleep or "
                          "already connected.")
            log("scan", f"attempt {i}/{attempts}: {kind}: {detail}")
            if i < attempts:
                await asyncio.sleep(scan_timeout / 4)  # let it resume advertising, then retry
    raise PocketError(f"Could not reach {address} after {attempts} tries: "
                      f"{type(last).__name__ if last else 'not found'}")


async def run_step(address: str, label: str, key: str | None, *, scan_timeout: float,
                   settle: float, attempts: int, redact, app_style: bool = True) -> dict:
    """One connection: optionally unlock with `key`, then read the battery to see if it took.
    Before disconnecting, tell the recorder to drop any AP it raised, so it advertises again."""
    result: dict = {"step": label, "connected": False, "sk_reply": None,
                     "battery": None, "answered_after_key": None, "dropped": False}
    link = None
    try:
        link = await open_link(address, scan_timeout=scan_timeout, attempts=attempts, redact=redact,
                               app_style=app_style)
        result["connected"] = True
        if key is not None:
            reply = await link.request(f"SK&{key}", "SK", timeout=5.0)
            result["sk_reply"] = reply
            log("key", f"APP&SK&<key> -> {('MCU&SK&' + reply) if reply is not None else 'no answer'}")
            await asyncio.sleep(settle)
            if not link.connected:
                result["dropped"] = True
                log("result", "the recorder dropped the link after the key")
                return result
        else:
            log("key", "sending no key")
        battery = await link.request("BAT", "BAT", timeout=5.0)
        result["battery"] = battery
        result["answered_after_key"] = battery is not None
        log("result", f"APP&BAT -> {('MCU&BAT&' + battery) if battery is not None else 'no answer'}"
                      f"  => {'unlocked' if battery is not None else 'not unlocked'}")
        if not link.connected:
            result["dropped"] = True
        elif key is not None:
            # Unlocking raises the WiFi AP; drop it so the recorder advertises again for the
            # next step. Best-effort: WIFIC may get no answer, which is fine.
            await link.request("WIFIC", "WIFIC", timeout=3.0)
    except PocketError as e:
        result["error"] = str(e)
        log("error", redact(str(e)))
    except (KeyboardInterrupt, asyncio.CancelledError):
        raise
    except Exception as e:  # noqa: BLE001 - surface anything bleak throws, keep going
        log_traceback()
        result["error"] = f"{type(e).__name__}: {e}"
        log("error", redact(result["error"]))
    finally:
        if link is not None:
            try:
                await link.close()
            except Exception:  # noqa: BLE001
                pass
    return result


def interpret(steps: list[tuple[str, str | None]], results: list[dict]) -> list[str]:
    """Plain-language reading of what the run showed."""
    notes: list[str] = []
    by_label = {r["step"]: r for r in results}

    def unlocked(label: str) -> bool | None:
        r = by_label.get(label)
        return None if r is None or not r["connected"] else r["answered_after_key"]

    if unlocked("none") is True:
        notes.append("The recorder answered commands with NO key: it isn't locked at all.")
    elif unlocked("none") is False:
        notes.append("Without a key the recorder did not answer commands: a key is required.")

    first_key = next((lbl for kind, lbl in zip((k for k, _ in steps), (r["step"] for r in results))
                      if kind in ("key", "other", "fresh")), None)
    if unlocked("key") is True:
        notes.append("The command-line key unlocked the recorder.")
    elif unlocked("key") is False:
        notes.append("The command-line key did NOT unlock the recorder.")

    if unlocked("fresh") is True and unlocked("key") is True:
        notes.append("A different (random) key ALSO unlocked it: the recorder accepts any key "
                     "(no binding to one key).")
    elif unlocked("fresh") is False and unlocked("key") is True:
        notes.append("A different key was refused while the command-line key worked: the recorder "
                     "is bound to the first key it accepted (first-key-wins).")

    key_results = [r for r in results if r["step"] in ("key", "fresh") or r["step"].startswith("other")]
    if len([r for r in key_results if r["answered_after_key"]]) and \
            any(not r["answered_after_key"] for r in key_results):
        notes.append("Not every key worked: order the steps were run in matters (see the timeline).")
    return notes


async def main_async(args) -> int:
    key = args.key or os.environ.get("POCKET_SESSION_KEY")
    steps = args.steps
    if any(kind == "key" for kind, _ in steps) and not key:
        print("A 'key' step needs a key: pass it as the second argument or set POCKET_SESSION_KEY.")
        return 2

    resolved: list[tuple[str, str | None]] = []
    used_fresh: list[str] = []
    for kind, value in steps:
        if kind == "none":
            resolved.append(("none", None))
        elif kind == "key":
            resolved.append(("key", key))
        elif kind == "fresh":
            k = fresh_key()
            used_fresh.append(k)
            resolved.append(("fresh", k))
        else:
            resolved.append((f"other:{value}", value))

    redact = redactor([key, *(key[:8] for _ in [key] if key), *used_fresh,
                       *(v for kind, v in steps if kind == "other" and v)])

    log("start", f"recorder {args.address}; steps: {', '.join(lbl for lbl, _ in resolved)}")
    log("note", "run this on a recorder you own; for a clean first-key test, factory reset it first")
    results: list[dict] = []
    for label, step_key in resolved:
        log("step", f"=== {label} ===")
        results.append(await run_step(args.address, label, step_key,
                                      scan_timeout=args.scan_timeout, settle=args.settle,
                                      attempts=args.scan_attempts, redact=redact,
                                      app_style=not args.legacy_write))
        await asyncio.sleep(args.between)

    print("\n=== Reading ===")
    for note in interpret(steps, results) or ["No clear conclusion; see the timeline above."]:
        print(f"  - {note}")

    if args.out:
        report = {
            "recorder": args.address,
            "when": dt.datetime.now().isoformat(timespec="seconds"),
            "steps": [lbl for lbl, _ in resolved],
            "results": [{k: v for k, v in r.items() if k != "key"} for r in results],
            "notes": interpret(steps, results),
        }
        with open(args.out, "w") as f:
            json.dump(report, f, indent=2)
        print(f"\nReport written to {args.out} (keys are not stored in it).")
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="pocket-keybind",
        description=__doc__.split("\n\n")[0],
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Example:\n"
               "  POCKET_SESSION_KEY=lzYjgY68j5fmiwAM pocket-keybind F4:4B:26:17:34:1D\n"
               "  pocket-keybind F4:4B:26:17:34:1D --steps none,fresh,fresh")
    p.add_argument("address", help="the recorder's Bluetooth address, AA:BB:CC:DD:EE:FF")
    p.add_argument("key", nargs="?", help="the session key for 'key' steps "
                   "(or set POCKET_SESSION_KEY to keep it out of your shell history)")
    p.add_argument("--steps", type=parse_steps, default=parse_steps("none,key,fresh,key"),
                   help="comma-separated: none, key, fresh, other:<KEY> (default none,key,fresh,key)")
    p.add_argument("--scan-timeout", type=float, default=20.0,
                   help="seconds to look for the recorder per scan attempt (default 20)")
    p.add_argument("--scan-attempts", type=int, default=4,
                   help="scan+connect tries per step; after unlocking the recorder raises its "
                        "WiFi AP and stops advertising for ~15 s (default 4)")
    p.add_argument("--settle", type=float, default=1.0,
                   help="seconds to wait after sending the key before testing (default 1)")
    p.add_argument("--between", type=float, default=15.0,
                   help="seconds between steps, to let the AP drop and BLE advertising resume "
                        "(default 15)")
    p.add_argument("--legacy-write", action="store_true",
                   help="write commands to 001120a3 without response (the old way our tools did it) "
                        "instead of the app's way: 001120a2 with response, after subscribing to "
                        "001120a3 and 001120a1")
    p.add_argument("--out", help="also write a JSON report here (keys are left out)")
    p.add_argument("--log", help="log file (default keybind-<time>.log here); it holds bleak's "
                                 "debug log, including the bytes written, so keep it private")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    if not re.fullmatch(r"[0-9A-Fa-f]{2}([:-][0-9A-Fa-f]{2}){5}|[0-9A-Fa-f]{8}-[0-9A-Fa-f-]{27}",
                        args.address):
        build_parser().error("the address should look like AA:BB:CC:DD:EE:FF")
    path = args.log or f"keybind-{dt.datetime.now():%Y%m%d-%H%M%S}.log"
    open_log(path)
    print(f"logging to {path}")
    try:
        return asyncio.run(main_async(args))
    except KeyboardInterrupt:
        log("stop", "interrupted")
        return 130
    finally:
        print(f"\nlog saved to {path}")


if __name__ == "__main__":
    import sys
    sys.exit(main())
