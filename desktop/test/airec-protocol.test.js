'use strict';

// Tests for the AIREC recorder's protocol helpers (desktop/src/airec-protocol.js): command
// framing, reply parsing, the recording name/size format, and the Ogg Opus packaging. The Ogg
// output is checked by re-parsing it: every page's CRC must recompute, and the packets must
// reassemble to exactly the input packets (validated against real recordings while writing it).
const test = require('node:test');
const assert = require('node:assert/strict');
const proto = require('../src/airec-protocol');

test('build frames a command: 55 AA <len> <cmd> <data>', () => {
  assert.deepEqual([...proto.build(0x0e)], [0x55, 0xaa, 0x01, 0x0e]);
  assert.deepEqual([...proto.build(0x34, new Uint8Array([0x00, 0x01]))], [0x55, 0xaa, 0x03, 0x34, 0x00, 0x01]);
});

test('FrameParser splits, joins and skips stray bytes', () => {
  const p = new proto.FrameParser();
  // AA 55 02 0E 64 (battery 100) then AA 55 01 06 (list end), fed in two halves.
  const whole = Uint8Array.from([0xaa, 0x55, 0x02, 0x0e, 0x64, 0xaa, 0x55, 0x01, 0x06]);
  assert.deepEqual(p.feed(whole.slice(0, 3)), []); // only a partial frame so far
  const frames = p.feed(whole.slice(3));
  assert.equal(frames.length, 2);
  assert.equal(frames[0].cmd, 0x0e);
  assert.deepEqual([...frames[0].data], [0x64]);
  assert.equal(frames[1].cmd, 0x06);
  assert.equal(frames[1].data.length, 0);
  // Bytes that don't start a frame are counted and dropped.
  const before = p.skipped;
  assert.deepEqual(p.feed(Uint8Array.from([0x00, 0x99])), []);
  assert.equal(p.skipped, before + 2);
});

test('parseEntry reads a 14-char name and a 4-byte big-endian size', () => {
  const data = Uint8Array.from([...Buffer.from('20261015183232'), 0x00, 0x01, 0x7b, 0xc0]);
  const { name, size } = proto.parseEntry(data);
  assert.equal(name, '20261015183232');
  assert.equal(size, 0x00017bc0);
  assert.equal(size, 97216);
});

test('nameToDate and recordingMeta', () => {
  const d = proto.nameToDate('20261015183232');
  assert.equal(d.getFullYear(), 2026);
  assert.equal(d.getMonth(), 9); // October
  assert.equal(d.getDate(), 15);
  assert.equal(d.getHours(), 18);
  assert.equal(proto.nameToDate('not-a-time'), null);
  const meta = proto.recordingMeta('20261015183232', 97216);
  assert.equal(meta.seconds, 24); // 97216 / 4000
  assert.ok(meta.recordedAt);
});

test('dateToName round-trips with nameToDate', () => {
  const d = new Date(2026, 9, 15, 18, 32, 32);
  assert.equal(proto.dateToName(d), '20261015183232');
});

// slot builds an 80-byte Opus packet like the recorder's: TOC 0x4b, code 3, one frame, pad zeros.
function slot(seed, pad = 10) {
  const b = new Uint8Array(proto.SLOT);
  b[0] = 0x4b; // config 9 (20 ms), mono, code 3
  b[1] = 0x41; // "A": 1 frame, padding present (0x40)
  b[2] = pad;
  for (let i = 3; i < proto.SLOT - pad; i += 1) b[i] = ((seed + i) % 255) + 1;
  return b;
}

test('slotOk accepts a valid packet and rejects stereo/short', () => {
  assert.ok(proto.slotOk(slot(1)));
  const stereo = slot(1).slice();
  stereo[0] = 0x4f; // stereo bit
  assert.ok(!proto.slotOk(stereo));
  assert.ok(!proto.slotOk(slot(1).slice(0, 40)));
});

test('splitSlots re-aligns past a damaged packet', () => {
  const good = [];
  for (let i = 0; i < 10; i += 1) good.push(slot(i, 5 + i));
  // A whole recording, but packet 4 is cut short by 16 bytes, so the boundaries shift there.
  const parts = good.map((p, i) => (i === 4 ? p.slice(0, proto.SLOT - 16) : p));
  const data = Buffer.concat(parts.map((p) => Buffer.from(p)));
  const { packets, resyncs } = proto.splitSlots(new Uint8Array(data));
  assert.equal(packets.length, 9); // the cut packet is skipped, the rest recovered
  assert.equal(resyncs.length, 1);
  assert.equal(resyncs[0], 4 * proto.SLOT);
});

// parseOgg re-parses an Ogg stream into its packets, checking each page's CRC.
function parseOgg(ogg) {
  let pos = 0;
  const packets = [];
  let cur = [];
  let pages = 0;
  while (pos < ogg.length) {
    assert.equal(String.fromCharCode(...ogg.slice(pos, pos + 4)), 'OggS');
    const nseg = ogg[pos + 26];
    const lacing = ogg.slice(pos + 27, pos + 27 + nseg);
    const bodyAt = pos + 27 + nseg;
    const bodyLen = lacing.reduce((n, l) => n + l, 0);
    const page = ogg.slice(pos, bodyAt + bodyLen);
    const stored = (page[22] | (page[23] << 8) | (page[24] << 16) | (page[25] << 24)) >>> 0;
    const zeroed = page.slice();
    zeroed[22] = zeroed[23] = zeroed[24] = zeroed[25] = 0;
    assert.equal(proto.oggCrc(zeroed), stored, `page ${pages} CRC`);
    let off = bodyAt;
    for (const l of lacing) {
      cur.push(ogg.slice(off, off + l));
      off += l;
      if (l < 255) {
        packets.push(Buffer.concat(cur.map((c) => Buffer.from(c))));
        cur = [];
      }
    }
    pages += 1;
    pos = bodyAt + bodyLen;
  }
  return { packets, pages };
}

test('packRecordingAsOgg: CRC and packet round-trip', () => {
  const slots = [];
  for (let i = 0; i < 120; i += 1) slots.push(slot(i, 5 + (i % 20)));
  const data = new Uint8Array(Buffer.concat(slots.map((p) => Buffer.from(p))));
  const { ogg, packets, resyncs } = proto.packRecordingAsOgg(data);
  assert.equal(packets, 120);
  assert.equal(resyncs.length, 0);

  const parsed = parseOgg(ogg);
  // First two packets are OpusHead and OpusTags.
  assert.equal(String.fromCharCode(...parsed.packets[0].slice(0, 8)), 'OpusHead');
  assert.equal(parsed.packets[0][9], 1); // one channel
  assert.equal(String.fromCharCode(...parsed.packets[1].slice(0, 8)), 'OpusTags');
  // The rest must be exactly the input packets, in order.
  const audio = parsed.packets.slice(2);
  assert.equal(audio.length, 120);
  for (let i = 0; i < slots.length; i += 1) assert.deepEqual([...audio[i]], [...slots[i]]);
});

test('packRecordingAsOgg: granule advances 320 samples per packet', () => {
  const slots = [];
  for (let i = 0; i < 3; i += 1) slots.push(slot(i));
  const data = new Uint8Array(Buffer.concat(slots.map((p) => Buffer.from(p))));
  const { ogg } = proto.packRecordingAsOgg(data);
  // The last page is the one with the end-of-stream header type (0x04); its granule is the total.
  let pos = 0;
  let lastGranule = -1;
  while (pos < ogg.length) {
    const nseg = ogg[pos + 26];
    const bodyLen = ogg.slice(pos + 27, pos + 27 + nseg).reduce((n, l) => n + l, 0);
    const headerType = ogg[pos + 5];
    const granule = ogg[pos + 6] | (ogg[pos + 7] << 8) | (ogg[pos + 8] << 16) | (ogg[pos + 9] << 24);
    if (headerType & 0x04) lastGranule = granule >>> 0;
    pos = pos + 27 + nseg + bodyLen;
  }
  assert.equal(lastGranule, 3 * 320);
});
