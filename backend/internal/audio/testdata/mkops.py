#!/usr/bin/env python3
"""Builds recorder.ops, the test file for ops_test.go: a synthetic signal (no recording of
anyone) encoded by libopus as SILK wideband 20 ms packets and laid out the way a Jieli-based
Pocket recorder writes its .ops files: raw packets, each padded to 80 bytes, except the last
one before a pause, which is written as it is.

    ffmpeg -f lavfi -i "sine=frequency=220:sample_rate=16000:duration=3,volume=0.5" \
        -af "vibrato=f=4:d=0.5" -ac 1 -ar 16000 -c:a libopus -application voip \
        -b:a 24k -frame_duration 20 -vbr off tone.opus
    python3 mkops.py tone.opus recorder.ops
"""
import struct
import sys

SLOT = 80


def ogg_packets(data):
    """Yields the packets of an Ogg stream (no packet spans pages in a small file)."""
    pos, buf = 0, b""
    while pos < len(data):
        assert data[pos:pos + 4] == b"OggS"
        nseg = data[pos + 26]
        segs = data[pos + 27:pos + 27 + nseg]
        body = pos + 27 + nseg
        for s in segs:
            buf += data[body:body + s]
            body += s
            if s < 255:
                yield buf
                buf = b""
        pos = body


def recorder_packet(pkt, pad):
    """Rewrites a single-frame packet as code 3 (one frame) with pad bytes of padding."""
    toc, frame = pkt[0], pkt[1:]
    if toc & 3 == 3:  # libopus pads its CBR packets: strip that padding
        assert pkt[1] & 0x3F == 1 and pkt[1] & 0x80 == 0
        frame, end = pkt[2:], len(pkt)
        if pkt[1] & 0x40:
            i = 2
            while pkt[i] == 255:
                end -= 254
                i += 1
            end -= pkt[i]
            frame = pkt[i + 1:end]
    else:
        assert toc & 3 == 0
    if pad == 0:
        return bytes([toc | 3, 0x01]) + frame
    assert pad <= 254
    return bytes([toc | 3, 0x41, pad - 1]) + frame + bytes(pad - 1)


def main(src, dst):
    pkts = list(ogg_packets(open(src, "rb").read()))[2:]  # skip OpusHead and OpusTags
    out = b""
    for i, p in enumerate(pkts):
        if i == 60:  # a pause: this packet isn't padded to the slot
            out += recorder_packet(p, 1)
            continue
        size = len(recorder_packet(p, 1))
        assert size <= SLOT, size
        out += recorder_packet(p, SLOT - size + 1)
    open(dst, "wb").write(out)
    print(len(pkts), "packets,", len(out), "bytes")


if __name__ == "__main__":
    main(*sys.argv[1:])
