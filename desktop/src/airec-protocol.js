// The AIREC recorder's Bluetooth protocol, kept free of Electron and Web Bluetooth so the tests
// can drive it and the hidden-window page (airec-ble.js) and the main process share it. The
// protocol is in tools/pocket-wifi-probe/AIREC.md. In short, over one GATT service:
//
//   command (write)  55 AA <len> <cmd> <data>     len = 1 + data length
//   reply   (notify) AA 55 <len> <cmd> <data>
//
// Recordings are named by their start time (YYYYMMDDHHMMSS) and stored as back-to-back 80-byte
// Opus packets (20 ms, 16 kHz mono); packRecordingAsOgg wraps them into a playable Ogg Opus file
// without needing an Opus decoder.
'use strict';

const SERVICE = '0011200a-2233-4455-6677-8899dfdedddc';
const LIVE = '0011201a-2233-4455-6677-8899dfdedddc';
const COMMAND = '0011202a-2233-4455-6677-8899dfdedddc';
const REPLY = '0011203a-2233-4455-6677-8899dfdedddc';
const FILE_DATA = '0011204a-2233-4455-6677-8899dfdedddc';

const NAME_RE = /^AIREC/i;

const SLOT = 80; // bytes per Opus packet in a recording
const FRAME_MS = 20;
const SAMPLE_RATE = 16000;
const SAMPLES_PER_PACKET = (SAMPLE_RATE * FRAME_MS) / 1000; // 320
const BYTES_PER_SECOND = (SLOT * 1000) / FRAME_MS; // 4000

// Commands used here.
const CMD = {
  serial: 0x01,
  setClock: 0x02,
  chipType: 0x20,
  firmware: 0x12,
  model: 0x29,
  settings: 0x26,
  state: 0x0f,
  battery: 0x0e,
  storage: 0x0b,
  storageFree: 0x0c,
  storageTotal: 0x0d,
  readClock: 0x30,
  listEntry: 0x05,
  listEnd: 0x06,
  download: 0x07,
  downloadDone: 0x09,
  preDelete: 0x34,
  delete: 0x0a,
  error: 0xfc,
};

// build makes a command frame.
function build(cmd, data = new Uint8Array(0)) {
  const body = data instanceof Uint8Array ? data : new Uint8Array(data);
  const out = new Uint8Array(4 + body.length);
  out[0] = 0x55;
  out[1] = 0xaa;
  out[2] = body.length + 1;
  out[3] = cmd;
  out.set(body, 4);
  return out;
}

// FrameParser splits the reply stream (AA 55 <len> <cmd> <data>) into {cmd, data} frames. A
// notification may hold several frames or part of one; bytes that don't start a frame are
// counted (skipped) and dropped.
class FrameParser {
  constructor() {
    this.buf = new Uint8Array(0);
    this.skipped = 0;
  }

  feed(chunk) {
    const merged = new Uint8Array(this.buf.length + chunk.length);
    merged.set(this.buf);
    merged.set(chunk, this.buf.length);
    this.buf = merged;
    const out = [];
    for (;;) {
      const i = indexOfPair(this.buf, 0xaa, 0x55);
      if (i < 0) {
        const keep = this.buf.length && this.buf[this.buf.length - 1] === 0xaa ? 1 : 0;
        this.skipped += this.buf.length - keep;
        this.buf = this.buf.slice(this.buf.length - keep);
        return out;
      }
      if (i > 0) {
        this.skipped += i;
        this.buf = this.buf.slice(i);
      }
      if (this.buf.length < 3) return out;
      const n = this.buf[2];
      if (n === 0) {
        // No command byte: not a frame. Step past this AA 55 and keep scanning.
        this.skipped += 2;
        this.buf = this.buf.slice(2);
        continue;
      }
      if (this.buf.length < 3 + n) return out;
      out.push({ cmd: this.buf[3], data: this.buf.slice(4, 3 + n) });
      this.buf = this.buf.slice(3 + n);
    }
  }
}

function indexOfPair(buf, a, b) {
  for (let i = 0; i + 1 < buf.length; i += 1) if (buf[i] === a && buf[i + 1] === b) return i;
  return -1;
}

// --- Parsing ------------------------------------------------------------------------------

const ascii = (bytes) => String.fromCharCode(...bytes).replace(/\0+$/, '');
const u32be = (bytes, at = 0) => ((bytes[at] << 24) | (bytes[at + 1] << 16) | (bytes[at + 2] << 8) | bytes[at + 3]) >>> 0;

// parseEntry reads a 14-character name and a 4-byte big-endian size (a list entry or a download
// header).
function parseEntry(data) {
  return { name: ascii(data.slice(0, 14)), size: u32be(data, 14) };
}

// nameToDate turns YYYYMMDDHHMMSS into a Date (local time), or null.
function nameToDate(name) {
  const m = /^(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(\d{2})$/.exec(name);
  if (!m) return null;
  const [, y, mo, d, h, mi, s] = m.map(Number);
  const date = new Date(y, mo - 1, d, h, mi, s);
  return Number.isNaN(date.getTime()) ? null : date;
}

// recordingMeta describes one listed recording for the UI.
function recordingMeta(name, size) {
  const date = nameToDate(name);
  return {
    name,
    size,
    seconds: Math.round(size / BYTES_PER_SECOND),
    recordedAt: date ? date.toISOString() : null,
  };
}

// dateToName is the recorder's clock format for `set clock`.
function dateToName(date) {
  const p = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}${p(date.getMonth() + 1)}${p(date.getDate())}${p(date.getHours())}${p(date.getMinutes())}${p(date.getSeconds())}`;
}

// --- Opus / Ogg ---------------------------------------------------------------------------

const TWENTY_MS_CONFIGS = new Set([1, 5, 9, 13, 15, 19, 23, 27, 31]); // Opus configs with 20 ms frames

// slotOk says whether 80 bytes read as one mono 20 ms Opus packet (code 0, or code 3 with one
// frame and zero padding filling the slot). The recorder sometimes stores a short packet; those
// don't check out, and splitSlots re-aligns past them, as the vendor app does.
function slotOk(b) {
  if (b.length < SLOT) return false;
  const toc = b[0];
  if (!TWENTY_MS_CONFIGS.has(toc >> 3) || toc & 0x04) return false;
  const code = toc & 0x03;
  if (code === 0) return true;
  if (code !== 3 || (b[1] & 0x3f) !== 1) return false;
  if (b[1] & 0x40) {
    const pad = b[2];
    if (pad >= SLOT - 3) return false;
    for (let i = SLOT - pad; i < SLOT; i += 1) if (b[i] !== 0) return false;
    return true;
  }
  return true;
}

// splitSlots returns {packets, resyncs}: the 80-byte Opus packets, and the offsets where a slot
// didn't read as a packet and the next start had to be found.
function splitSlots(data) {
  const packets = [];
  const resyncs = [];
  let pos = 0;
  while (pos + SLOT <= data.length) {
    if (slotOk(data.subarray(pos, pos + SLOT))) {
      packets.push(data.subarray(pos, pos + SLOT));
      pos += SLOT;
      continue;
    }
    resyncs.push(pos);
    let next = -1;
    for (let p = pos + 1; p + SLOT <= data.length; p += 1) {
      if (slotOk(data.subarray(p, p + SLOT)) && (p + 2 * SLOT > data.length || slotOk(data.subarray(p + SLOT, p + 2 * SLOT)))) {
        next = p;
        break;
      }
    }
    if (next < 0) break;
    pos = next;
  }
  return { packets, resyncs };
}

// Ogg CRC: polynomial 0x04c11db7, MSB-first, no reflection, init 0, no final xor (libogg's).
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let i = 0; i < 256; i += 1) {
    let r = (i << 24) >>> 0;
    for (let k = 0; k < 8; k += 1) r = r & 0x80000000 ? ((r << 1) ^ 0x04c11db7) >>> 0 : (r << 1) >>> 0;
    t[i] = r >>> 0;
  }
  return t;
})();

function oggCrc(data) {
  let r = 0;
  for (let i = 0; i < data.length; i += 1) r = (((r << 8) >>> 0) ^ CRC_TABLE[((r >>> 24) ^ data[i]) & 0xff]) >>> 0;
  return r >>> 0;
}

function writeU32(arr, at, value) {
  arr[at] = value & 0xff;
  arr[at + 1] = (value >>> 8) & 0xff;
  arr[at + 2] = (value >>> 16) & 0xff;
  arr[at + 3] = (value >>> 24) & 0xff;
}

// oggPage builds one Ogg page from its segments (each segment one whole packet here).
function oggPage(headerType, granule, serial, seq, segments) {
  const lacing = [];
  for (const seg of segments) {
    let n = seg.length;
    while (n >= 255) {
      lacing.push(255);
      n -= 255;
    }
    lacing.push(n);
  }
  const bodyLength = segments.reduce((n, s) => n + s.length, 0);
  const page = new Uint8Array(27 + lacing.length + bodyLength);
  page.set([0x4f, 0x67, 0x67, 0x53]); // "OggS"
  page[4] = 0;
  page[5] = headerType;
  // Granule position is 64-bit little-endian; our values fit in 32 bits.
  writeU32(page, 6, granule >>> 0);
  writeU32(page, 10, Math.floor(granule / 0x100000000) >>> 0);
  writeU32(page, 14, serial >>> 0);
  writeU32(page, 18, seq >>> 0);
  // 22..26 CRC, left zero for now.
  page[26] = lacing.length;
  page.set(lacing, 27);
  let at = 27 + lacing.length;
  for (const seg of segments) {
    page.set(seg, at);
    at += seg.length;
  }
  writeU32(page, 22, oggCrc(page));
  return page;
}

const SERIAL = 0x4b4e4f50; // "KNOP"
const PACKETS_PER_PAGE = 50;

// packRecordingAsOgg wraps a recording's raw bytes into an Ogg Opus file (a Uint8Array). It needs
// no Opus decoder: each 80-byte packet becomes one Ogg packet, granule advancing 320 samples.
// Returns { ogg, packets, resyncs, seconds }.
function packRecordingAsOgg(data) {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  const { packets, resyncs } = splitSlots(bytes);

  const head = new Uint8Array(19);
  head.set([0x4f, 0x70, 0x75, 0x73, 0x48, 0x65, 0x61, 0x64]); // "OpusHead"
  head[8] = 1; // version
  head[9] = 1; // channel count
  // pre-skip (10..12) = 0, input sample rate (12..16), output gain (16..18) = 0, mapping family (18) = 0
  writeU32(head, 12, SAMPLE_RATE);

  const vendor = new TextEncoder().encode('knowpod');
  const tags = new Uint8Array(8 + 4 + vendor.length + 4);
  tags.set([0x4f, 0x70, 0x75, 0x73, 0x54, 0x61, 0x67, 0x73]); // "OpusTags"
  writeU32(tags, 8, vendor.length);
  tags.set(vendor, 12);
  writeU32(tags, 12 + vendor.length, 0); // 0 user comments

  const pages = [oggPage(0x02, 0, SERIAL, 0, [head]), oggPage(0x00, 0, SERIAL, 1, [tags])];
  let seq = 2;
  let samples = 0;
  for (let i = 0; i < packets.length; i += PACKETS_PER_PAGE) {
    const group = packets.slice(i, i + PACKETS_PER_PAGE);
    samples += SAMPLES_PER_PACKET * group.length;
    const last = i + PACKETS_PER_PAGE >= packets.length;
    pages.push(oggPage(last ? 0x04 : 0x00, samples, SERIAL, seq, group));
    seq += 1;
  }
  const total = pages.reduce((n, p) => n + p.length, 0);
  const ogg = new Uint8Array(total);
  let at = 0;
  for (const p of pages) {
    ogg.set(p, at);
    at += p.length;
  }
  return { ogg, packets: packets.length, resyncs, seconds: Math.round(bytes.length / BYTES_PER_SECOND) };
}

module.exports = {
  SERVICE,
  LIVE,
  COMMAND,
  REPLY,
  FILE_DATA,
  NAME_RE,
  SLOT,
  SAMPLE_RATE,
  BYTES_PER_SECOND,
  CMD,
  build,
  FrameParser,
  parseEntry,
  nameToDate,
  dateToName,
  recordingMeta,
  slotOk,
  splitSlots,
  oggCrc,
  oggPage,
  packRecordingAsOgg,
};
