"""Listen for the recorder reaching out to us, in case the transfer runs the other way round."""
from __future__ import annotations

import asyncio

from ..util import parse_ports, preview
from . import monitor


class _Udp(asyncio.DatagramProtocol):
    def __init__(self, port, events, log):
        self.port, self.events, self.log = port, events, log

    def datagram_received(self, data, addr):
        self.log("inbound", f"UDP {self.port} from {addr[0]}:{addr[1]}, {len(data)} bytes")
        self.events.append({"proto": "udp", "port": self.port, "from": f"{addr[0]}:{addr[1]}", **preview(data)})


@monitor("inbound", help="TCP and UDP listeners on --listen-ports; logs anything that connects")
async def inbound(ctx, stop: asyncio.Event):
    bind = ctx.local_ip or "0.0.0.0"
    events: list[dict] = []
    failed: dict[str, str] = {}
    servers, transports = [], []

    def tcp_handler(port):
        async def handle(reader, writer):
            peer = writer.get_extra_info("peername")
            ctx.log("inbound", f"TCP {port} connection from {peer[0]}:{peer[1]}")
            try:
                data = await asyncio.wait_for(reader.read(65536), 5)
            except (asyncio.TimeoutError, OSError):
                data = b""
            events.append({"proto": "tcp", "port": port, "from": f"{peer[0]}:{peer[1]}", **preview(data)})
            writer.close()
        return handle

    loop = asyncio.get_running_loop()
    for port in parse_ports(ctx.args.listen_ports):
        try:
            servers.append(await asyncio.start_server(tcp_handler(port), bind, port))
        except OSError as e:
            failed[f"tcp/{port}"] = str(e)
        try:
            t, _ = await loop.create_datagram_endpoint(lambda p=port: _Udp(p, events, ctx.log), local_addr=(bind, port))
            transports.append(t)
        except OSError as e:
            failed[f"udp/{port}"] = str(e)
    ctx.log("inbound", f"listening on {len(servers)} TCP and {len(transports)} UDP ports at {bind}"
                       + (f" ({len(failed)} could not bind)" if failed else ""))
    try:
        await stop.wait()
    finally:
        for s in servers:
            s.close()
        for t in transports:
            t.close()
    return {"bound_to": bind, "events": events, "bind_failures": failed,
            "note": "a firewall (e.g. Windows Defender) may block these unless allowed"}
