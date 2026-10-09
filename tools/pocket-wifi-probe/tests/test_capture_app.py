"""Tests for capture_app.py's offline parts: the HCI decoder and the WiFi line matcher."""
import importlib.util
import io
import struct
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location("capture_app", Path(__file__).parent.parent / "capture_app.py")
capture_app = sys.modules["capture_app"] = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(capture_app)

PHONE, DEVICE = 0, 1  # btsnoop flags bit 0: 0 = sent by the phone's host, 1 = received
ADDR = bytes.fromhex("ff1717264cf4")  # F4:4C:26:17:17:FF, little-endian on the air
CMD_UUID = bytes.fromhex("785634129988776655443322a3201100")  # 001120a3-2233-4455-6677-889912345678


def event(code: int, params: bytes) -> bytes:
    return bytes([0x04, code, len(params)]) + params


def acl(handle: int, payload: bytes, cid: int, pb: int = 0x2) -> bytes:
    l2cap = struct.pack("<HH", len(payload), cid) + payload
    return bytes([0x02]) + struct.pack("<HH", handle | pb << 12, len(l2cap)) + l2cap


def att(handle: int, payload: bytes) -> bytes:
    return acl(handle, payload, 0x0004)


def adv_report(name: bytes) -> bytes:
    ad = bytes([len(name) + 1, 0x09]) + name + bytes([3, 0xFF, 0x34, 0x12])
    return event(0x3E, bytes([0x02, 1, 0x00, 0x00]) + ADDR + bytes([len(ad)]) + ad + b"\xc0")


def decode(packets: list[tuple[int, bytes]]) -> list[tuple]:
    out = []
    dec = capture_app.HciDecoder(lambda kind, text, detail=None, quiet=False: out.append((kind, text, quiet)),
                                 addrs=())
    for flags, pkt in packets:
        dec.packet(flags, pkt)
    return out


def loud(events: list[tuple]) -> list[str]:
    return [f"{k} {t}" for k, t, q in events if not q]


def test_activation_like_session():
    h = 0x40
    conn = event(0x3E, bytes([0x01, 0x00]) + struct.pack("<H", h) + bytes([0x00, 0x00]) + ADDR + bytes(6))
    packets = [
        (DEVICE, adv_report(b"PKT01_TEST")),
        (DEVICE, conn),
        (PHONE, att(h, b"\x10\x01\x00\xff\xff\x00\x28")),  # read by group type: primary services
        (DEVICE, att(h, b"\x11\x14" + struct.pack("<HH", 0x10, 0x16) + bytes.fromhex("785634129988776655443322a0201100"))),
        (PHONE, att(h, b"\x08\x10\x00\x16\x00\x03\x28")),  # read by type: characteristics
        (DEVICE, att(h, b"\x09\x15" + struct.pack("<HBH", 0x11, 0x14, 0x12) + CMD_UUID)),
        (PHONE, att(h, b"\x04\x13\x00\x13\x00")),
        (DEVICE, att(h, b"\x05\x01\x13\x00\x02\x29")),  # 0x0013 is a CCCD
        (PHONE, att(h, b"\x12\x13\x00\x01\x00")),
        (PHONE, acl(h, b"\x01\x03\x00\x0d\x10\x07\x07", 0x0006)),  # SMP pairing request
        (PHONE, att(h, b"\x52\x12\x00APP&SK&abcdefgh12345678")),
        (DEVICE, att(h, b"\x1b\x12\x00MCU&SK&OK\x00")),
        (PHONE, att(h, b"\x0a\x12\x00")),
        (DEVICE, att(h, b"\x0b\x01\x02\x03")),
        (DEVICE, event(0x05, bytes([0x00]) + struct.pack("<H", h) + bytes([0x13]))),
    ]
    lines = loud(decode(packets))
    assert lines[0] == "adv PKT01_TEST (F4:4C:26:17:17:FF) advertises: mfr=1234: name=PKT01_TEST"
    assert "connected to PKT01_TEST (F4:4C:26:17:17:FF)" in lines[1]
    assert "service 001120a0-2233-4455-6677-889912345678 at 0x0010-0x0016" in lines[2]
    assert lines[3].endswith("characteristic 001120a3-2233-4455-6677-889912345678 at 0x0012 [write-no-rsp,notify]")
    assert lines[4] == "phone> PKT01_TEST subscribe (notify) 001120a3"
    assert lines[5].startswith("phone> PKT01_TEST pairing request: io=NoInputNoOutput, bonding+MITM+secure connections")
    assert lines[6] == "phone> PKT01_TEST write-cmd 001120a3 (0x0012): APP&SK&abcdefgh12345678"
    assert lines[7] == "device< PKT01_TEST notify 001120a3 (0x0012): MCU&SK&OK"
    assert lines[8] == "device< PKT01_TEST read 001120a3 (0x0012): 010203 (3 B)"
    assert lines[9] == "bt disconnected from PKT01_TEST (reason 0x13)"
    assert capture_app.redact(lines[6]).endswith("APP&SK&<redacted>")


def test_fragmented_acl_and_binary_notification_flood():
    h = 0x41
    payload = b"\x1b\x20\x00" + bytes(range(100))
    whole = att(h, payload)
    first, rest = whole[:30], whole[30:]
    # The continuation fragment has its own ACL header with PB = 01.
    cont = bytes([0x02]) + struct.pack("<HH", h | 0x1 << 12, len(rest)) + rest
    first = first[:3] + struct.pack("<H", len(first) - 5) + first[5:]
    events = decode([(DEVICE, first), (DEVICE, cont)])
    assert loud(events) == [f"device< link 0x041 notify 0x0020: {bytes(range(48)).hex()}… (100 B)"]

    flood = decode([(DEVICE, att(h, b"\x1b\x20\x00\x00\xff"))] * 1000)
    assert len(loud(flood)) == capture_app.NOTIFY_SHOWN + 2  # then the 500th and the 1000th


def test_btsnoop_file_round_trip(tmp_path):
    pkt = adv_report(b"PKT01_FILE")
    data = b"btsnoop\0" + struct.pack(">II", 1, 1002)
    ts = capture_app.BTSNOOP_UNIX_OFFSET_US + 1_700_000_000_000_000
    data += struct.pack(">IIIIq", len(pkt), len(pkt), DEVICE | 2, 0, ts) + pkt
    f = tmp_path / "x.btsnoop"
    f.write_bytes(data)
    out = io.StringIO()
    assert capture_app.decode_file(f, out) == 1
    assert "advertises: mfr=1234: name=PKT01_FILE" in out.getvalue()


def test_wifi_monitor_matching(monkeypatch, tmp_path):
    said = []
    monkeypatch.setattr(capture_app, "say", lambda kind, text, detail=None, quiet=False: said.append(text))
    m = capture_app.Monitor("wlan0", None, ["a4:c1:38"], tmp_path / "x.pcap", None)
    m._freq = lambda mhz: None
    m.handle_line("12:00:00.1 1.0 Mb/s 2437 MHz 11g -60dBm signal BSSID:11:22:33:44:55:66 "
                  "DA:ff:ff:ff:ff:ff:ff SA:11:22:33:44:55:66 Beacon (HomeNet) [1.0* Mbit] ESS CH: 6\n")
    assert said == [] and m.frames == 0
    m.handle_line("12:00:00.2 1.0 Mb/s 2412 MHz 11b -50dBm signal BSSID:ff:ff:ff:ff:ff:ff "
                  "DA:ff:ff:ff:ff:ff:ff SA:a4:c1:38:00:00:01 Probe Request (HomeNet) [1.0 Mbit]\n")
    assert m.locked is None  # probe requests go out on every channel
    assert "recorder looks for network 'HomeNet' on 2412 MHz" in said
    m.handle_line("12:00:01.0 1.0 Mb/s 2462 MHz 11g -40dBm signal BSSID:a4:c1:38:00:00:01 "
                  "DA:ff:ff:ff:ff:ff:ff SA:a4:c1:38:00:00:01 Beacon () [1.0* Mbit] ESS CH: 11\n")
    assert m.locked == 2462
    assert "recorder announces network '<hidden>' on 2462 MHz" in said
    m.handle_line("12:00:02.0 2462 MHz BSSID:a4:c1:38:00:00:01 SA:a4:c1:38:00:00:01 "
                  "DA:aa:bb:cc:dd:ee:ff Data IV:1 EAPOL key (3) v2, len 117\n")
    assert m.eapol == 1


def _fake_forward(behaviour):
    """A local listener that acts like `adb forward` in one of its states; returns its port."""
    import socket
    import threading
    srv = socket.socket()
    srv.bind(("127.0.0.1", 0))
    srv.listen()

    def serve():
        conn, _ = srv.accept()
        behaviour(conn)
        srv.close()

    threading.Thread(target=serve, daemon=True).start()
    return srv.getsockname()[1]


SNOOP_HEADER = b"btsnoop\0" + struct.pack(">II", 1, 1002)


def test_connect_snoop_nothing_listening_on_the_phone():
    port = _fake_forward(lambda conn: conn.close())  # adb accepts, then closes
    sock, header, why = capture_app.connect_snoop(port, wait=0)
    assert sock is None and "nothing listens" in why


def test_connect_snoop_wrong_answer():
    port = _fake_forward(lambda conn: (conn.sendall(b"HTTP/1.1 400 x\r\n\r\n"), conn.close()))
    sock, _, why = capture_app.connect_snoop(port, wait=0)
    assert sock is None and "not with a btsnoop header" in why


def test_connect_snoop_streams_through_the_same_connection(tmp_path):
    import threading
    pkt = adv_report(b"PKT01_LIVE")
    record = struct.pack(">IIIIq", len(pkt), len(pkt), DEVICE | 2, 0, capture_app.BTSNOOP_UNIX_OFFSET_US) + pkt
    done = threading.Event()

    def phone(conn):
        conn.sendall(SNOOP_HEADER[:5])  # the header and records arrive in arbitrary pieces
        conn.sendall(SNOOP_HEADER[5:] + record[:10])
        conn.sendall(record[10:])
        done.wait(5)
        conn.close()

    port = _fake_forward(phone)
    sock, header, why = capture_app.connect_snoop(port, wait=0)
    assert sock is not None and header == SNOOP_HEADER and why == ""
    events = []
    dec = capture_app.HciDecoder(lambda kind, text, detail=None, quiet=False: events.append(text), addrs=())
    stream = capture_app.SnoopStream(tmp_path / "live.btsnoop", sock, header, dec)
    stream.start()
    assert stream.first.wait(5)
    done.set()
    stream.join(5)
    assert dec.packets == 1 and any("PKT01_LIVE" in e for e in events)
    assert (tmp_path / "live.btsnoop").read_bytes() == SNOOP_HEADER + record
