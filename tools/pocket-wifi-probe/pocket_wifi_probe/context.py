"""What a check gets to work with, and the report everything ends up in."""
from __future__ import annotations

import argparse
import datetime as dt
import json
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .hostwifi import HostWifi
from .pocket import PocketLink


class Report:
    """A timeline of everything that happened plus each check's result, written as JSON."""

    def __init__(self, secrets: list[str], verbose: bool = True):
        self.start = time.monotonic()
        self.started_at = dt.datetime.now().astimezone().isoformat(timespec="seconds")
        self.secrets = [s for s in secrets if s]
        self.verbose = verbose
        self.timeline: list[dict] = []
        self.results: dict[str, dict] = {}
        self.meta: dict[str, Any] = {}

    def redact(self, text: str) -> str:
        for s in sorted(self.secrets, key=len, reverse=True):
            text = text.replace(s, "<redacted>")
        return text

    def elapsed(self, t: float | None = None) -> float:
        return round((t if t is not None else time.monotonic()) - self.start, 3)

    def log(self, kind: str, text: str, t: float | None = None) -> None:
        text = self.redact(text)
        entry = {"t": self.elapsed(t), "kind": kind, "text": text}
        self.timeline.append(entry)
        if self.verbose:
            print(f"[{entry['t']:8.2f}] {kind:<12} {text}", flush=True)

    def result(self, phase: str, name: str, value: Any) -> None:
        self.results.setdefault(phase, {})[name] = value

    def write(self, path: Path) -> None:
        data = {
            "started_at": self.started_at,
            "meta": self.meta,
            "results": self.results,
            "timeline": self.timeline,
        }
        text = self.redact(json.dumps(data, indent=2, default=str))
        path.write_text(text, encoding="utf-8")


@dataclass
class Context:
    """Handed to every check and monitor."""
    args: argparse.Namespace
    report: Report
    pocket: PocketLink
    wifi: HostWifi
    host: str  # the recorder's address on its access point
    phase: str = "ble"
    local_ip: str | None = None
    ssid: str | None = None
    # Checks leave things here for later ones, e.g. "open_tcp" from tcp-scan.
    shared: dict[str, Any] = field(default_factory=dict)
    out_dir: Path = Path(".")

    def log(self, kind: str, text: str) -> None:
        self.report.log(kind, text)
