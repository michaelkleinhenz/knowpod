// Talks to a knowpod recorder (the ESP32 gadget, esp32/) over Bluetooth LE, with Web Bluetooth:
// the recorder offers its recordings while it has no Wi-Fi, and the app relays them to the
// backend (recorder-relay.js; the protocol is in docs/ble-transfer.md). Runs in a hidden window
// of its own (recorder-ble.html), which recorder-bluetooth.js opens for one connection; the
// main process picks the recorder when the page asks for a device (select-bluetooth-device) and
// answers the pairing (setBluetoothPairingHandler), so no chooser is shown.
//
// The app writes one JSON request at a time to CONTROL; the recorder answers in notifications of
// CONTROL, each [flags][bytes] (flags bit 0: more follow). File bytes of a read come as
// notifications of DATA, each [offset: uint32 LE][bytes].
'use strict';

const SERVICE = '6b6e7000-0b1e-4d0a-9c3e-6b6e6f77706f';
const CONTROL = '6b6e7001-0b1e-4d0a-9c3e-6b6e6f77706f';
const DATA = '6b6e7002-0b1e-4d0a-9c3e-6b6e6f77706f';

// LinkError carries a code for the app to explain (see recorder-bluetooth.js).
class LinkError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

const failure = (err) => ({
  ok: false,
  error: err instanceof LinkError ? err.code : err && err.name === 'SecurityError' ? 'auth' : 'failed',
  message: String((err && err.message) || err),
});

// toBase64 encodes bytes for the trip to the main process.
function toBase64(bytes) {
  let text = '';
  for (let i = 0; i < bytes.length; i += 0x8000) text += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(text);
}

// The open connection, or null.
let link = null;

// open connects to the recorder the main process picks and subscribes to both characteristics.
// Resolves to {ok, name} or {ok: false, error, message}.
async function open() {
  close();
  try {
    if (!navigator.bluetooth) throw new LinkError('unsupported', 'Web Bluetooth is not available');
    let device;
    try {
      device = await navigator.bluetooth.requestDevice({ filters: [{ services: [SERVICE] }] });
    } catch (err) {
      // The main process cancels the request when the recorder wasn't found in time.
      if (err && err.name === 'NotFoundError') throw new LinkError('not-found', err.message);
      throw err;
    }
    const server = await device.gatt.connect();
    const service = await server.getPrimaryService(SERVICE);
    const control = await service.getCharacteristic(CONTROL);
    const data = await service.getCharacteristic(DATA);

    const state = {
      device,
      control,
      fragments: [], // of the response being received
      response: null, // resolves the request waiting for a response
      collector: null, // the read waiting for file bytes
      disconnected: false,
    };
    control.addEventListener('characteristicvaluechanged', (event) => {
      const view = event.target.value;
      const bytes = new Uint8Array(view.buffer, view.byteOffset, view.byteLength).slice();
      if (!bytes.length) return;
      state.fragments.push(bytes.subarray(1));
      if (bytes[0] & 1) return;
      const all = new Uint8Array(state.fragments.reduce((n, f) => n + f.length, 0));
      let at = 0;
      for (const f of state.fragments) {
        all.set(f, at);
        at += f.length;
      }
      state.fragments = [];
      let message = null;
      try {
        message = JSON.parse(new TextDecoder().decode(all));
      } catch {
        // garbled: the request times out
      }
      if (message && state.response) state.response(message);
    });
    data.addEventListener('characteristicvaluechanged', (event) => {
      const c = state.collector;
      const view = event.target.value;
      if (!c || view.byteLength < 4) return;
      const offset = view.getUint32(0, true);
      // A gap means a notification got lost: keep what came before it; the app asks again.
      if (offset !== c.next) {
        c.gap = true;
        return;
      }
      if (c.gap) return;
      const bytes = new Uint8Array(view.buffer.slice(view.byteOffset + 4, view.byteOffset + view.byteLength));
      c.chunks.push(bytes);
      c.next += bytes.length;
    });
    device.addEventListener('gattserverdisconnected', () => {
      state.disconnected = true;
      state.response?.(null);
    });
    await control.startNotifications();
    await data.startNotifications();
    link = state;
    return { ok: true, name: device.name || '' };
  } catch (err) {
    close();
    return failure(err);
  }
}

// exchange writes a request and resolves to the recorder's response, or rejects after timeout
// ms or when the recorder disconnects.
function exchange(message, timeout) {
  const state = link;
  if (!state || state.disconnected) return Promise.reject(new LinkError('disconnected', 'The recorder disconnected'));
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      state.response = null;
      reject(new LinkError('timeout', `No answer to ${message.op}`));
    }, timeout);
    state.response = (response) => {
      clearTimeout(timer);
      state.response = null;
      if (response) resolve(response);
      else reject(new LinkError('disconnected', 'The recorder disconnected'));
    };
    state.fragments = [];
    // The first write to a paired-only characteristic starts the pairing if the system has no
    // bond yet (the main process answers it).
    state.control.writeValueWithResponse(new TextEncoder().encode(JSON.stringify(message))).catch((err) => {
      clearTimeout(timer);
      state.response = null;
      reject(err);
    });
  });
}

// request sends message and resolves to {ok: true, response} or {ok: false, error, message}.
async function request(message, timeout = 15_000) {
  try {
    return { ok: true, response: await exchange(message, timeout) };
  } catch (err) {
    return failure(err);
  }
}

// read asks for length bytes of recording id from offset and resolves to {ok: true, data}
// (base64; the bytes received in order, possibly fewer) or {ok: false, error, message}.
async function read(id, offset, length, timeout = 60_000) {
  const state = link;
  if (!state) return failure(new LinkError('disconnected', 'No open connection'));
  const collector = { next: offset, chunks: [], gap: false };
  state.collector = collector;
  try {
    const response = await exchange({ op: 'read', id, offset, length }, timeout);
    if (!response.ok) return { ok: false, error: response.error || 'failed', message: response.message || '' };
    const bytes = new Uint8Array(collector.next - offset);
    let at = 0;
    for (const chunk of collector.chunks) {
      bytes.set(chunk, at);
      at += chunk.length;
    }
    return { ok: true, data: toBase64(bytes) };
  } catch (err) {
    return failure(err);
  } finally {
    if (state.collector === collector) state.collector = null;
  }
}

// close drops the connection.
function close() {
  try {
    link?.device.gatt.disconnect();
  } catch {
    // already gone
  }
  link = null;
  return { ok: true };
}

window.recorderBluetooth = { open, request, read, close };
