// Relays the recordings of a knowpod recorder (the ESP32 gadget, esp32/) to the backend: the
// recorder hands them over Bluetooth while it has no Wi-Fi (see docs/ble-transfer.md), and this
// uploads each one with the recorder's own device token, as the recorder would itself
// (docs/device-protocol.md). The backend then files it under the device and deduplicates it by
// its recordingId, so a recording relayed here and later sent again over Wi-Fi is stored once.
//
// The Bluetooth side is a link: request(message, timeout) resolves to the recorder's response,
// read(id, offset, length) to the bytes it sent (see recorder-bluetooth.js). Kept free of
// Electron, so the tests can drive it with a fake recorder and server.
'use strict';

// Bytes per PATCH to the backend.
const uploadChunk = 1024 * 1024;
// Bytes per Bluetooth read request (the recorder's maximum).
const readChunk = 256 * 1024;
// How long the recorder may take to hash a recording ("open").
const openTimeout = 180_000;
// How often a checksum mismatch may start an upload over.
const maxChecksumResets = 2;

const complete = (status) => ['received', 'stored', 'transcribed', 'summarized'].includes(status);

// RelayError is a failure that ends the relay: code is no-token (the recorder has no device
// token), other-server (it uploads to another server than the app's), token (the backend refused
// the token), recorder (the recorder answered with an error), or http (the backend failed).
class RelayError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

// originOf is the origin of url, or null.
function originOf(url) {
  try {
    const u = new URL(url);
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.origin : null;
  } catch {
    return null;
  }
}

// ask sends a request to the recorder and returns its response, throwing when it failed.
async function ask(link, message, timeout) {
  const response = await link.request(message, timeout);
  if (!response || response.ok !== true) {
    throw new RelayError('recorder', `${message.op}: ${response?.message || response?.error || 'no answer'}`);
  }
  return response;
}

// readRange reads length bytes of a recording from offset, in requests the recorder can serve.
async function readRange(link, id, offset, length) {
  const parts = [];
  let got = 0;
  while (got < length) {
    const part = await link.read(id, offset + got, Math.min(readChunk, length - got));
    if (!part.length) throw new RelayError('recorder', `read: no data at ${offset + got}`);
    parts.push(part);
    got += part.length;
  }
  return Buffer.concat(parts, length);
}

// relayRecordings uploads the recordings the recorder offers. options:
//   link          the Bluetooth link to the recorder
//   serverUrl     the app's server (an origin); the recorder must upload there
//   fetch         a fetch() for the backend
//   onProgress({phase, current, total, title, bytes, totalBytes})   phase: listing, preparing,
//                 uploading
//   isCancelled() true stops after the current step
// Resolves to {copied, failed, total, errors: [{id, message}]}; rejects with a RelayError when
// nothing can be relayed (see RelayError), or with the link's error when the recorder went away.
async function relayRecordings({ link, serverUrl, fetch, onProgress = () => {}, isCancelled = () => false }) {
  const info = await ask(link, { op: 'info' });
  if (!info.token || !info.backend) throw new RelayError('no-token', 'The recorder has no device token');
  const backend = String(info.backend).replace(/\/+$/, '');
  if (originOf(backend) !== originOf(serverUrl)) {
    throw new RelayError('other-server', `The recorder uploads to ${originOf(backend) || backend}`);
  }

  // api calls the backend's device API with the recorder's token; resolves to {status, body,
  // headers}, throwing only when the backend can't be reached.
  async function api(method, path, { json, data, headers = {} } = {}) {
    const res = await fetch(`${backend}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${info.token}`,
        ...(json ? { 'Content-Type': 'application/json' } : {}),
        ...(data ? { 'Content-Type': 'application/offset+octet-stream' } : {}),
        ...headers,
      },
      body: json ? JSON.stringify(json) : data,
    });
    const text = await res.text();
    let body = {};
    try {
      body = text ? JSON.parse(text) : {};
    } catch {
      // not JSON
    }
    if (res.status === 401) throw new RelayError('token', 'The backend refused the recorder’s device token');
    return { status: res.status, body, offset: Number(res.headers.get('Upload-Offset') ?? NaN) };
  }

  const httpError = (what, r) =>
    new RelayError('http', `${what}: HTTP ${r.status}${r.body?.error ? ` (${r.body.error})` : ''}`);

  onProgress({ phase: 'listing', current: 0, total: 0 });
  const { recordings = [] } = await ask(link, { op: 'list' });
  const result = { copied: 0, failed: 0, total: recordings.length, errors: [] };

  for (const [i, rec] of recordings.entries()) {
    if (isCancelled()) break;
    const progress = { current: i + 1, total: recordings.length, title: rec.title || rec.id };
    onProgress({ ...progress, phase: 'preparing' });
    const { size, sha256 } = await ask(link, { op: 'open', id: rec.id }, openTimeout);

    const create = () =>
      api('POST', '/uploads', {
        json: {
          recordingId: rec.id,
          size,
          sha256,
          ...(rec.recordedAt ? { recordedAt: rec.recordedAt } : {}),
          highlights: (rec.highlights || []).map((ms) => ({ offsetMs: ms })),
        },
      });

    // fail gives up on this recording; the recorder keeps offering it.
    const fail = (message) => {
      result.failed++;
      result.errors.push({ id: rec.id, message });
    };

    let created = await create();
    if (created.status === 409) {
      fail('The backend already has a different recording with this id');
      continue;
    }
    if (created.status !== 200 && created.status !== 201) throw httpError('Creating the upload', created);
    let { uploadId, status, offset = 0 } = created.body;
    let resets = 0;
    let failed = false;

    while (!complete(status) && !isCancelled()) {
      if (status === 'failed') {
        fail(`The backend rejected the recording: ${created.body.error || ''}`.trim());
        failed = true;
        break;
      }
      onProgress({ ...progress, phase: 'uploading', bytes: offset, totalBytes: size });
      const n = Math.min(uploadChunk, size - offset);
      let r;
      if (n <= 0) {
        // Everything was sent but not confirmed: ask where the upload stands.
        r = await api('GET', `/uploads/${uploadId}`);
      } else {
        const data = await readRange(link, rec.id, offset, n);
        r = await api('PATCH', `/uploads/${uploadId}`, { data, headers: { 'Upload-Offset': String(offset) } });
      }
      if (r.status === 200) {
        status = r.body.status;
        offset = Number.isFinite(r.body.offset) ? r.body.offset : Number.isFinite(r.offset) ? r.offset : offset + Math.max(n, 0);
        if (n <= 0 && !complete(status) && status !== 'failed' && offset >= size) {
          throw httpError('The backend has the whole file but did not finish it', r);
        }
        created = r;
      } else if (r.status === 409) {
        // Offset mismatch: continue from the backend's.
        offset = Number.isFinite(r.body.offset) ? r.body.offset : r.offset;
        if (!Number.isFinite(offset)) throw httpError('Upload offset', r);
      } else if (r.status === 404) {
        // Gone on the backend (purged): start a new upload.
        created = await create();
        if (created.status !== 200 && created.status !== 201) throw httpError('Creating the upload', created);
        ({ uploadId, status, offset = 0 } = created.body);
      } else if (r.status === 422) {
        // Checksum mismatch (the backend reset the offset to 0) or not a supported file.
        if (++resets > maxChecksumResets) {
          fail(`Backend: ${r.body.error || 'checksum mismatch'}`);
          failed = true;
          break;
        }
        offset = 0;
      } else {
        throw httpError('Uploading', r);
      }
    }
    if (failed || !complete(status)) continue;

    await ask(link, { op: 'done', id: rec.id, uploadId, status });
    result.copied++;
    onProgress({ ...progress, phase: 'uploading', bytes: size, totalBytes: size });
  }
  return result;
}

module.exports = { relayRecordings, RelayError, originOf, uploadChunk, readChunk };
