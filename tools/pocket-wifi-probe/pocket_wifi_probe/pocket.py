"""Bluetooth LE link to a Pocket recorder (heypocketai.com).

The recorder speaks ASCII on one characteristic: the probe writes "APP&<command>" and the
recorder answers "MCU&<command>&<value>" in notifications of the same characteristic. Some
answers come unasked (WIFIS status changes, OFF), so every message is kept, with the time it
arrived, and callers wait for the first one matching what they want.
"""
from __future__ import annotations

import asyncio
import re
import time
from dataclasses import dataclass
from typing import Callable

from bleak import BleakClient, BleakScanner

SERVICE = "001120a0-2233-4455-6677-889912345678"
COMMAND = "001120a3-2233-4455-6677-889912345678"


class PocketError(Exception):
    pass


@dataclass
class Message:
    t: float  # monotonic seconds
    text: str  # e.g. "MCU&WIFIS&3"
    source: str  # characteristic UUID it came on

    def value(self, name: str) -> str | None:
        """The value of "MCU&<name>&<value>", or None if this isn't that answer."""
        prefix = f"MCU&{name}&"
        if self.text.startswith(prefix):
            return self.text[len(prefix):]
        if self.text == f"MCU&{name}":
            return ""
        return None


def split_messages(text: str) -> list[str]:
    """One notification may carry several MCU& messages back to back."""
    text = text.replace("\0", "").strip()
    parts = [p.strip() for p in re.split(r"(?=MCU&)", text)]
    return [p for p in parts if p]


class PocketLink:
    """An open connection to the recorder. Use as `async with PocketLink(address) as pocket`."""

    def __init__(self, address: str, *, scan_timeout: float = 20.0, notify_all: bool = False,
                 notify: list[str] | None = None,
                 on_message: Callable[[Message], None] | None = None,
                 on_event: Callable[[str, str], None] | None = None):
        self.address = address
        self.scan_timeout = scan_timeout
        self.notify_all = notify_all
        self.notify = [n.lower() for n in notify or []]
        self.on_message = on_message
        self.on_event = on_event or (lambda kind, text: None)
        self.client: BleakClient | None = None
        self.messages: list[Message] = []
        self.disconnected = False
        self._new = asyncio.Event()
        self._write_lock = asyncio.Lock()
        self._subscribed: list[str] = []
        self._last: dict[str, tuple[float, str]] = {}
        self.duplicates = 0
        # Binary notifications (Bluetooth file data on 001120a1), complete, per characteristic.
        self.data: dict[str, bytearray] = {}

    async def __aenter__(self) -> "PocketLink":
        device = await BleakScanner.find_device_by_address(self.address, timeout=self.scan_timeout)
        if device is None:
            raise PocketError(f"Recorder {self.address} not found (is it awake and nearby?)")
        self.client = BleakClient(device, disconnected_callback=self._on_disconnect)
        await self.client.connect()
        self.on_event("ble", f"connected to {device.name or '?'} ({device.address})")
        # Only the command characteristic here: the recorder drops a connection that isn't
        # unlocked within a few seconds, so the others wait for subscribe_others().
        await self._subscribe(COMMAND)
        if COMMAND not in self._subscribed:
            raise PocketError("Could not subscribe to the command characteristic")
        return self

    async def subscribe_others(self) -> None:
        """Subscribes to the other notify/indicate characteristics, one by one, logging each:
        all of them with notify_all, else those whose UUID starts with one in `notify`."""
        for s in self.client.services:
            for c in s.characteristics:
                if c.uuid in self._subscribed or not {"notify", "indicate"} & set(c.properties):
                    continue
                if not self.notify_all and not any(c.uuid.startswith(n) for n in self.notify):
                    continue
                if not self.connected:
                    raise PocketError("The recorder dropped the connection while subscribing")
                if await self._subscribe(c.uuid):
                    self.on_event("ble", f"subscribed to {c.uuid}")

    async def _subscribe(self, uuid: str) -> bool:
        try:
            # BlueZ can hang here once the recorder has gone; don't wait forever.
            await asyncio.wait_for(self.client.start_notify(uuid, self._handler(uuid)), 5)
        except Exception as e:  # one refusing characteristic shouldn't stop the probe
            self.on_event("ble", f"could not subscribe to {uuid}: {type(e).__name__} {e}")
            return False
        self._subscribed.append(uuid)
        return True

    async def __aexit__(self, *exc) -> None:
        await self.close()

    async def close(self) -> None:
        if self.client is None or not self.client.is_connected:
            return
        for uuid in self._subscribed:
            try:
                await self.client.stop_notify(uuid)
            except Exception:
                pass
        try:
            await self.client.disconnect()
        except Exception as e:
            self.on_event("ble", f"disconnect failed: {e}")
        else:
            self.on_event("ble", "disconnected")

    def _handler(self, uuid: str):
        def handle(_sender, data: bytearray) -> None:
            raw = bytes(data)
            text = raw.decode("ascii", errors="replace")
            binary = uuid != COMMAND and not text.startswith("MCU&")
            if binary:
                # Not the text protocol; keep it readable.
                text = f"<{len(raw)} bytes> {raw[:64].hex()}"
                parts = [text]
            else:
                parts = split_messages(text)
            now = time.monotonic()
            for part in parts:
                # The recorder (or BlueZ) delivers each notification several times within a few
                # milliseconds; keep one. Real repeats, like polled WIFIS, are seconds apart.
                last = self._last.get(uuid)
                if last and last[1] == part and now - last[0] < 0.05:
                    self.duplicates += 1
                    continue
                self._last[uuid] = (now, part)
                if binary:
                    self.data.setdefault(uuid, bytearray()).extend(raw)
                msg = Message(now, part, uuid)
                self.messages.append(msg)
                if self.on_message:
                    self.on_message(msg)
            self._new.set()
        return handle

    def _on_disconnect(self, _client) -> None:
        self.disconnected = True
        self.on_event("ble", "recorder dropped the connection")
        self._new.set()

    @property
    def connected(self) -> bool:
        return bool(self.client and self.client.is_connected and not self.disconnected)

    def mark(self) -> int:
        """An index into messages; wait_for(since=mark) only looks at messages after it."""
        return len(self.messages)

    async def send(self, command: str) -> int:
        """Writes "APP&<command>" and returns the mark before it, to wait for its answer."""
        if not self.connected:
            raise PocketError("Not connected to the recorder")
        async with self._write_lock:
            since = self.mark()
            self.on_event("ble>", f"APP&{command}")
            await self.client.write_gatt_char(COMMAND, f"APP&{command}".encode("ascii"), response=False)
            return since

    async def wait_for(self, predicate: Callable[[Message], bool], *, since: int,
                       timeout: float) -> Message | None:
        """The first message after `since` that matches, or None after timeout."""
        deadline = time.monotonic() + timeout
        index = since
        while True:
            while index < len(self.messages):
                msg = self.messages[index]
                index += 1
                if source_is_command(msg) and predicate(msg):
                    return msg
            remaining = deadline - time.monotonic()
            if remaining <= 0 or self.disconnected:
                return None
            self._new.clear()
            try:
                await asyncio.wait_for(self._new.wait(), remaining)
            except asyncio.TimeoutError:
                pass

    async def request(self, command: str, answer: str, *, timeout: float = 5.0,
                      accept: Callable[[str], bool] | None = None) -> str | None:
        """Sends a command and returns the value of the first "MCU&<answer>&<value>"."""
        since = await self.send(command)

        def match(m: Message) -> bool:
            v = m.value(answer)
            return v is not None and (accept is None or accept(v))

        msg = await self.wait_for(match, since=since, timeout=timeout)
        return None if msg is None else msg.value(answer)

    async def collect(self, command: str, *, until: str, timeout: float = 10.0) -> list[Message]:
        """Sends a command and gathers messages until one starting "MCU&<until>" arrives."""
        since = await self.send(command)
        end = await self.wait_for(lambda m: m.value(until) is not None, since=since, timeout=timeout)
        stop = self.messages.index(end) + 1 if end else len(self.messages)
        return [m for m in self.messages[since:stop] if source_is_command(m)]

    def services(self) -> list[dict]:
        """The GATT table as plain data."""
        out = []
        for s in self.client.services:
            out.append({
                "uuid": s.uuid,
                "handle": s.handle,
                "description": s.description,
                "characteristics": [
                    {"uuid": c.uuid, "handle": c.handle, "properties": list(c.properties),
                     "description": c.description}
                    for c in s.characteristics
                ],
            })
        return out


def source_is_command(msg: Message) -> bool:
    return msg.source == COMMAND
