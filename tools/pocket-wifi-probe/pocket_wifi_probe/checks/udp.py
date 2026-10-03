"""UDP: what answers datagrams, what the recorder's DNS server says, and service discovery."""
from __future__ import annotations

import asyncio
import random
import socket
import struct
import time

from ..util import parse_ports, preview
from . import check

# Datagrams tried on every --udp-ports port. Add guesses here.
DATAGRAMS = [
    ("empty", b""),
    ("range-request", b"RANGE request bytes=0-"),
    ("zeros", b"\0" * 8),
]


async def udp_exchange(host: str, port: int, payload: bytes, wait: float = 1.0,
                       local: str | None = None) -> dict:
    """Sends one datagram on a connected socket and collects replies. A closed port shows up
    as an ICMP port-unreachable, which a connected socket reports as refused/reset."""
    loop = asyncio.get_running_loop()
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.setblocking(False)
    replies = []
    try:
        if local:
            sock.bind((local, 0))
        sock.connect((host, port))
        await loop.sock_sendall(sock, payload)
        end = time.monotonic() + wait
        while (left := end - time.monotonic()) > 0:
            try:
                data = await asyncio.wait_for(loop.sock_recv(sock, 65535), left)
            except asyncio.TimeoutError:
                break
            replies.append(preview(data))
    except (ConnectionRefusedError, ConnectionResetError):
        return {"state": "closed"}
    except OSError as e:
        return {"state": "error", "error": f"{type(e).__name__}: {e}"}
    finally:
        sock.close()
    return {"state": "answered" if replies else "open|filtered", "replies": replies}


@check("udp-probe", phases=("ready", "begin"), help="datagrams to --udp-ports, noting replies and ICMP closes")
async def udp_probe(ctx):
    sem = asyncio.Semaphore(32)
    out = {}

    async def one(port, name, payload):
        async with sem:
            res = await udp_exchange(ctx.host, port, payload, local=ctx.local_ip)
        if res["state"] == "answered":
            ctx.log("udp-probe", f"{port} answered '{name}'")
        out[f"{port}/{name}"] = res

    await asyncio.gather(*(one(p, n, d) for p in parse_ports(ctx.args.udp_ports) for n, d in DATAGRAMS))
    summary: dict[str, list[int]] = {}
    for key, res in out.items():
        summary.setdefault(res["state"], []).append(int(key.split("/")[0]))
    return {"summary": {k: sorted(set(v)) for k, v in summary.items()}, "detail": out}


# --- DNS ---------------------------------------------------------------------------------

def dns_query(name: str, qtype: int = 1, qclass: int = 1, ident: int | None = None) -> bytes:
    ident = random.randrange(65536) if ident is None else ident
    header = struct.pack(">HHHHHH", ident, 0x0100, 1, 0, 0, 0)  # recursion desired
    labels = b"".join(bytes([len(p)]) + p.encode() for p in name.strip(".").split(".") if p)
    return header + labels + b"\0" + struct.pack(">HH", qtype, qclass)


def _skip_name(data: bytes, i: int) -> int:
    while i < len(data):
        n = data[i]
        if n == 0:
            return i + 1
        if n & 0xC0 == 0xC0:
            return i + 2
        i += n + 1
    return i


def dns_parse(data: bytes) -> dict:
    """Header, and the answer records' type and data (A records as addresses)."""
    if len(data) < 12:
        return {"error": "short reply", "raw": preview(data)}
    ident, flags, qd, an, ns, ar = struct.unpack(">HHHHHH", data[:12])
    out = {"id": ident, "rcode": flags & 0xF, "authoritative": bool(flags & 0x400),
           "counts": [qd, an, ns, ar], "answers": []}
    i = 12
    try:
        for _ in range(qd):
            i = _skip_name(data, i) + 4
        for _ in range(an + ns + ar):
            i = _skip_name(data, i)
            rtype, _, ttl, rdlen = struct.unpack(">HHIH", data[i:i + 10])
            rdata = data[i + 10:i + 10 + rdlen]
            i += 10 + rdlen
            value = socket.inet_ntoa(rdata) if rtype == 1 and rdlen == 4 else preview(rdata)
            out["answers"].append({"type": rtype, "ttl": ttl, "data": value})
    except (struct.error, OSError):
        out["truncated"] = True
    return out


# Names to ask the recorder's DNS server. Add guesses here.
DNS_NAMES = ["pocket.local", "pocket", "heypocketai.com", "api.heypocketai.com",
             "connectivitycheck.gstatic.com", "captive.apple.com", "www.msftconnecttest.com", "example.com"]


@check("dns", phases=("ready",), help="ask the recorder's DNS server (port 53) a few names, over UDP and TCP")
async def dns(ctx):
    out = {}
    for name in DNS_NAMES:
        res = await udp_exchange(ctx.host, 53, dns_query(name), wait=1.5, local=ctx.local_ip)
        out[name] = [dns_parse(bytes.fromhex(r["hex"])) for r in res.get("replies", [])] or res
    res = await udp_exchange(ctx.host, 53, dns_query("version.bind", qtype=16, qclass=3), local=ctx.local_ip)
    out["version.bind (CHAOS TXT)"] = [dns_parse(bytes.fromhex(r["hex"])) for r in res.get("replies", [])] or res

    # Same over TCP (length-prefixed): port 53 is the one TCP port known to be open.
    q = dns_query(DNS_NAMES[0])
    try:
        reader, writer = await asyncio.wait_for(asyncio.open_connection(ctx.host, 53), 3)
        writer.write(struct.pack(">H", len(q)) + q)
        await writer.drain()
        size = struct.unpack(">H", await asyncio.wait_for(reader.readexactly(2), 3))[0]
        out["tcp"] = dns_parse(await asyncio.wait_for(reader.readexactly(size), 3))
        writer.close()
    except (OSError, asyncio.TimeoutError, asyncio.IncompleteReadError, struct.error) as e:
        out["tcp"] = {"error": f"{type(e).__name__}: {e}"}
    return out


# --- Discovery ---------------------------------------------------------------------------

SSDP = (b"M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\n"
        b"MX: 2\r\nST: ssdp:all\r\n\r\n")


async def multicast(local: str, group: str, port: int, payload: bytes, wait: float = 3.0) -> list[dict]:
    loop = asyncio.get_running_loop()
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.setblocking(False)
    replies = []
    try:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        sock.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_IF, socket.inet_aton(local))
        sock.bind((local, 0))
        await loop.sock_sendto(sock, payload, (group, port))
        end = time.monotonic() + wait
        while (left := end - time.monotonic()) > 0:
            try:
                data, addr = await asyncio.wait_for(loop.sock_recvfrom(sock, 65535), left)
            except asyncio.TimeoutError:
                break
            replies.append({"from": f"{addr[0]}:{addr[1]}", **preview(data)})
    except OSError as e:
        replies.append({"error": f"{type(e).__name__}: {e}"})
    finally:
        sock.close()
    return replies


@check("discovery", phases=("ready", "begin"), help="mDNS, SSDP and subnet broadcast; who answers")
async def discovery(ctx):
    if not ctx.local_ip:
        return {"skipped": "no address on the recorder's network"}
    broadcast = ctx.host.rsplit(".", 1)[0] + ".255"
    mdns_q = dns_query("_services._dns-sd._udp.local", qtype=12, ident=0)
    results = await asyncio.gather(
        multicast(ctx.local_ip, "224.0.0.251", 5353, mdns_q),
        udp_exchange(ctx.host, 5353, mdns_q, local=ctx.local_ip),
        multicast(ctx.local_ip, "239.255.255.250", 1900, SSDP),
        multicast(ctx.local_ip, broadcast, 9, b"RANGE request bytes=0-"),
    )
    return dict(zip(["mdns", "mdns-unicast", "ssdp", "broadcast"], results))
