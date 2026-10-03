"""Checks and monitors the probe runs. Add one by dropping a module in this package.

A check is an async function taking a Context and returning something JSON-serialisable
for the report. Register it with @check and say in which phases it runs:

    ble    connected and unlocked over Bluetooth, before anything WiFi
    ready  joined to the recorder's access point (WIFIS=1, or as close as it got)
    begin  after APP&U&WIFI, the command the app sends to start the transfer

A monitor is an async function taking a Context and an asyncio.Event; it runs in the
background from joining the access point until teardown, and should return once the event
is set. Register it with @monitor.

    from . import check

    @check("my-check", phases=("ready", "begin"), help="What it finds out")
    async def my_check(ctx):
        ctx.log("my-check", "something worth seeing live")
        return {"answer": 42}

Checks run in the order their modules sort, then in the order they are defined. Every module
here is imported by load(), so nothing else has to be edited.
"""
from __future__ import annotations

import importlib
import pkgutil
from dataclasses import dataclass, field
from typing import Awaitable, Callable

PHASES = ("ble", "ready", "begin")


@dataclass
class Check:
    name: str
    func: Callable[..., Awaitable[object]]
    phases: tuple[str, ...]
    help: str
    default: bool = True
    kind: str = "check"  # or "monitor"
    order: int = field(default=0)


REGISTRY: dict[str, Check] = {}


def _register(item: Check) -> None:
    if item.name in REGISTRY:
        raise ValueError(f"Check {item.name} registered twice")
    for p in item.phases:
        if p not in PHASES:
            raise ValueError(f"Check {item.name}: unknown phase {p}")
    item.order = len(REGISTRY)
    REGISTRY[item.name] = item


def check(name: str, *, phases: tuple[str, ...], help: str, default: bool = True):
    def wrap(func):
        _register(Check(name, func, tuple(phases), help, default))
        return func
    return wrap


def monitor(name: str, *, help: str, default: bool = True):
    def wrap(func):
        _register(Check(name, func, (), help, default, kind="monitor"))
        return func
    return wrap


def load() -> dict[str, Check]:
    for mod in sorted(pkgutil.iter_modules(__path__), key=lambda m: m.name):
        importlib.import_module(f"{__name__}.{mod.name}")
    return REGISTRY


def select(only: list[str] | None, skip: list[str]) -> list[Check]:
    """The checks to run: the named ones, or every default one, minus the skipped."""
    items = sorted(load().values(), key=lambda c: c.order)
    unknown = [n for n in (only or []) + skip if n not in REGISTRY]
    if unknown:
        raise ValueError(f"Unknown check(s): {', '.join(unknown)} (see --list-checks)")
    if only:
        items = [c for c in items if c.name in only]
    else:
        items = [c for c in items if c.default]
    return [c for c in items if c.name not in skip]
