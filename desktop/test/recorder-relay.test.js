// Tests for the Bluetooth relay of the knowpod recorder (src/recorder-relay.js) against a fake
// recorder that answers like esp32/src/net/ble.cpp and a fake backend that follows the device
// upload protocol (docs/device-protocol.md).
// Run with: npm test (plain node:test, no Electron needed).
'use strict';

const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const { test } = require('node:test');

const { relayRecordings, RelayError, readChunk } = require('../src/recorder-relay');

const TOKEN = 'kpd_test';
const SERVER = 'https://knowpod.example';

const audio = (n, seed = 0) => {
  const out = Buffer.alloc(n);
  for (let i = 0; i < n; i++) out[i] = (i * 13 + seed) & 0xff;
  return out;
};
const sha = (buf) => crypto.createHash('sha256').update(buf).digest('hex');

// fakeRecorder holds files by id and answers the requests of docs/ble-transfer.md.
function fakeRecorder(files, { backend = `${SERVER}/api/v1`, token = TOKEN, shortReads = false } = {}) {
  const done = [];
  const reads = [];
  const link = {
    request: async (m) => {
      switch (m.op) {
        case 'info':
          return { ok: true, op: 'info', protocol: 1, name: 'knowpod-1A2B', backend, token, pending: files.size };
        case 'list':
          return {
            ok: true,
            op: 'list',
            recordings: [...files.entries()]
              .filter(([id]) => !done.some((d) => d.id === id))
              .map(([id, buf]) => ({ id, size: buf.length, recordedAt: '2026-09-26T08:15:00Z', highlights: [1500] })),
          };
        case 'open': {
          const buf = files.get(m.id);
          return buf ? { ok: true, op: 'open', id: m.id, size: buf.length, sha256: sha(buf) } : { ok: false, op: 'open', error: 'not-found' };
        }
        case 'done':
          done.push(m);
          return { ok: true, op: 'done', id: m.id };
        default:
          return { ok: false, op: m.op, error: 'bad-request' };
      }
    },
    read: async (id, offset, length) => {
      assert.ok(length <= readChunk, 'reads stay within what the recorder serves');
      reads.push({ id, offset, length });
      const n = shortReads ? Math.min(length, 1000) : length;
      return files.get(id).subarray(offset, offset + n);
    },
  };
  return { link, done, reads };
}

// fakeBackend keeps uploads like the service does; hooks can change single responses.
function fakeBackend({ onPatch } = {}) {
  const uploads = new Map(); // uploadId -> {recordingId, size, sha256, data, status, highlights}
  const calls = [];
  let next = 1;
  const reply = (status, body, headers = {}) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } });
  const view = (id, u) => ({ uploadId: id, recordingId: u.recordingId, status: u.status, size: u.size, offset: u.data.length });

  const fetch = async (url, init) => {
    const { pathname } = new URL(url);
    calls.push(`${init.method} ${pathname}`);
    if (init.headers.Authorization !== `Bearer ${TOKEN}`) return reply(401, { error: 'invalid token' });
    if (init.method === 'POST' && pathname === '/api/v1/uploads') {
      const req = JSON.parse(init.body);
      for (const [id, u] of uploads) {
        if (u.recordingId !== req.recordingId) continue;
        if (u.size !== req.size || u.sha256 !== req.sha256) return reply(409, { error: 'conflict' });
        return reply(200, view(id, u));
      }
      const id = `up${next++}`;
      uploads.set(id, { ...req, data: Buffer.alloc(0), status: 'uploading' });
      return reply(201, view(id, uploads.get(id)));
    }
    const id = pathname.split('/').pop();
    const u = uploads.get(id);
    if (!u) return reply(404, { error: 'unknown upload' });
    if (init.method === 'GET') return reply(200, view(id, u));
    if (init.method === 'PATCH') {
      const hooked = onPatch?.(u, init);
      if (hooked) return hooked;
      const offset = Number(init.headers['Upload-Offset']);
      if (offset !== u.data.length) return reply(409, view(id, u), { 'Upload-Offset': String(u.data.length) });
      u.data = Buffer.concat([u.data, Buffer.from(init.body)]);
      if (u.data.length === u.size) {
        if (sha(u.data) !== u.sha256) {
          u.data = Buffer.alloc(0);
          return reply(422, { error: 'checksum mismatch' });
        }
        u.status = 'received';
      }
      return reply(200, view(id, u), { 'Upload-Offset': String(u.data.length) });
    }
    return reply(405, {});
  };
  return { fetch, uploads, calls };
}

test('relays every offered recording and reports it done', async () => {
  const files = new Map([
    ['20260926-101500', audio(2_500_000)],
    ['20260926-120000', audio(300, 5)],
  ]);
  const recorder = fakeRecorder(files);
  const backend = fakeBackend();
  const progress = [];
  const result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: backend.fetch, onProgress: (p) => progress.push(p.phase) });

  assert.deepEqual(result, { copied: 2, failed: 0, total: 2, errors: [] });
  assert.equal(recorder.done.length, 2);
  for (const d of recorder.done) {
    const u = backend.uploads.get(d.uploadId);
    assert.equal(u.status, 'received');
    assert.equal(d.status, 'received');
    assert.ok(u.data.equals(files.get(d.id)));
    assert.deepEqual(u.highlights, [{ offsetMs: 1500 }]);
    assert.equal(u.recordedAt, '2026-09-26T08:15:00Z');
  }
  // 2.5 MB in 1 MiB PATCHes
  assert.equal(backend.calls.filter((c) => c.startsWith('PATCH')).length, 4);
  assert.ok(progress.includes('uploading'));
});

test('resumes an upload where the backend stands', async () => {
  const buf = audio(1_500_000);
  const files = new Map([['rec-0001', buf]]);
  const backend = fakeBackend();
  // An earlier attempt got 600 000 bytes through.
  backend.uploads.set('up0', { recordingId: 'rec-0001', size: buf.length, sha256: sha(buf), data: buf.subarray(0, 600_000), status: 'uploading' });
  const recorder = fakeRecorder(files);
  const result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: backend.fetch });

  assert.equal(result.copied, 1);
  assert.equal(recorder.reads[0].offset, 600_000);
  assert.ok(backend.uploads.get('up0').data.equals(buf));
});

test('follows the backend offset after a mismatch and copes with short reads', async () => {
  const buf = audio(5_000);
  const backend = fakeBackend();
  let first = true;
  backend.uploads.set('up0', { recordingId: 'a', size: buf.length, sha256: sha(buf), data: Buffer.alloc(0), status: 'uploading' });
  const original = backend.fetch;
  // The create call reports offset 0, but the backend already got 2 000 bytes meanwhile.
  const fetch = async (url, init) => {
    if (init.method === 'PATCH' && first) {
      first = false;
      backend.uploads.get('up0').data = buf.subarray(0, 2_000);
    }
    return original(url, init);
  };
  const recorder = fakeRecorder(new Map([['a', buf]]), { shortReads: true });
  const result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch });
  assert.equal(result.copied, 1);
  assert.ok(backend.uploads.get('up0').data.equals(buf));
});

test('a recording the backend already has is reported done without sending it', async () => {
  const buf = audio(1000);
  const backend = fakeBackend();
  backend.uploads.set('up0', { recordingId: 'a', size: buf.length, sha256: sha(buf), data: buf, status: 'stored' });
  const recorder = fakeRecorder(new Map([['a', buf]]));
  const result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: backend.fetch });
  assert.equal(result.copied, 1);
  assert.equal(recorder.reads.length, 0);
  assert.deepEqual(recorder.done, [{ op: 'done', id: 'a', uploadId: 'up0', status: 'stored' }]);
});

test('a conflicting recording id fails that recording only', async () => {
  const backend = fakeBackend();
  backend.uploads.set('up0', { recordingId: 'a', size: 1, sha256: 'x', data: Buffer.alloc(0), status: 'uploading' });
  const recorder = fakeRecorder(new Map([['a', audio(100)], ['b', audio(100, 1)]]));
  const result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: backend.fetch });
  assert.equal(result.copied, 1);
  assert.equal(result.failed, 1);
  assert.equal(result.errors[0].id, 'a');
  assert.deepEqual(recorder.done.map((d) => d.id), ['b']);
});

test('starts over after a checksum mismatch, and gives up when it repeats', async () => {
  const buf = audio(3000);
  // corrupt garbles the first `left` full sends; the backend then discards them (422).
  const garbling = (left) =>
    fakeBackend({
      onPatch: (_u, init) => {
        if (left-- > 0) init.body = Buffer.alloc(init.body.length);
        return null;
      },
    });

  let recorder = fakeRecorder(new Map([['a', buf]]));
  let result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: garbling(1).fetch });
  assert.equal(result.copied, 1);

  recorder = fakeRecorder(new Map([['b', buf]]));
  result = await relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: garbling(Infinity).fetch });
  assert.equal(result.copied, 0);
  assert.equal(result.failed, 1);
  assert.equal(recorder.done.length, 0);
});

test('refuses a recorder without a token or for another server', async () => {
  const backend = fakeBackend();
  await assert.rejects(
    relayRecordings({ link: fakeRecorder(new Map(), { token: '' }).link, serverUrl: SERVER, fetch: backend.fetch }),
    (err) => err instanceof RelayError && err.code === 'no-token',
  );
  await assert.rejects(
    relayRecordings({ link: fakeRecorder(new Map(), { backend: 'https://elsewhere.example/api/v1' }).link, serverUrl: SERVER, fetch: backend.fetch }),
    (err) => err instanceof RelayError && err.code === 'other-server',
  );
  assert.equal(backend.calls.length, 0);
});

test('stops when the backend refuses the device token', async () => {
  const recorder = fakeRecorder(new Map([['a', audio(10)]]), { token: 'kpd_revoked' });
  await assert.rejects(
    relayRecordings({ link: recorder.link, serverUrl: SERVER, fetch: fakeBackend().fetch }),
    (err) => err instanceof RelayError && err.code === 'token',
  );
  assert.equal(recorder.done.length, 0);
});
