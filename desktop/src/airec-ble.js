// Talks to an AIREC recorder over Bluetooth LE with Web Bluetooth, in a hidden window that
// airec-bluetooth.js opens (the main process picks the device, so no chooser shows). This page is
// sandboxed and has no Node, so it does only the transport: connect, subscribe, write command
// bytes, and buffer the notifications. All protocol logic (framing, the Ogg packaging) runs in
// the main process with airec-protocol.js, which drives this page.
//
//   open()               connect and subscribe to the reply, file-data and live characteristics
//   send(base64)         write command bytes to the command characteristic (no response)
//   poll(replyCursor)    new reply bytes since replyCursor, plus the file-data length so far
//   takeFileData()       the file bytes received so far, and clears them
//   clear()              drop buffered reply and file bytes before a new operation
//   close()
'use strict';

const SERVICE = '0011200a-2233-4455-6677-8899dfdedddc';
const COMMAND = '0011202a-2233-4455-6677-8899dfdedddc';
const REPLY = '0011203a-2233-4455-6677-8899dfdedddc';
const FILE_DATA = '0011204a-2233-4455-6677-8899dfdedddc';
const LIVE = '0011201a-2233-4455-6677-8899dfdedddc';

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

function toBase64(bytes) {
  let text = '';
  for (let i = 0; i < bytes.length; i += 0x8000) text += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(text);
}

function fromBase64(text) {
  const binary = atob(text);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function bytesOf(event) {
  const view = event.target.value;
  return new Uint8Array(view.buffer, view.byteOffset, view.byteLength).slice();
}

let link = null;

async function open() {
  close();
  try {
    if (!navigator.bluetooth) throw new LinkError('unsupported', 'Web Bluetooth is not available');
    let device;
    try {
      device = await navigator.bluetooth.requestDevice({ filters: [{ namePrefix: 'AIREC' }], optionalServices: [SERVICE] });
    } catch (err) {
      if (err && err.name === 'NotFoundError') throw new LinkError('not-found', err.message);
      throw err;
    }
    const server = await device.gatt.connect();
    const service = await server.getPrimaryService(SERVICE);
    const command = await service.getCharacteristic(COMMAND);
    const reply = await service.getCharacteristic(REPLY);
    const fileData = await service.getCharacteristic(FILE_DATA);

    const state = {
      device,
      command,
      replyBytes: [], // chunks of reply notifications
      replyLength: 0,
      fileChunks: [],
      fileLength: 0,
      liveLength: 0,
      disconnected: false,
    };
    reply.addEventListener('characteristicvaluechanged', (event) => {
      const bytes = bytesOf(event);
      if (!bytes.length) return;
      state.replyBytes.push(bytes);
      state.replyLength += bytes.length;
    });
    fileData.addEventListener('characteristicvaluechanged', (event) => {
      const bytes = bytesOf(event);
      if (!bytes.length) return;
      state.fileChunks.push(bytes);
      state.fileLength += bytes.length;
    });
    device.addEventListener('gattserverdisconnected', () => {
      state.disconnected = true;
    });
    await reply.startNotifications();
    await fileData.startNotifications();
    // The live characteristic is optional; failing to subscribe to it mustn't stop a download.
    try {
      const live = await service.getCharacteristic(LIVE);
      await live.startNotifications();
      live.addEventListener('characteristicvaluechanged', (event) => {
        state.liveLength += bytesOf(event).length;
      });
    } catch {
      // no live characteristic on this firmware: fine
    }
    link = state;
    return { ok: true, name: device.name || '', mtu: null };
  } catch (err) {
    close();
    return failure(err);
  }
}

async function send(base64) {
  const state = link;
  if (!state || state.disconnected) return failure(new LinkError('disconnected', 'The recorder disconnected'));
  try {
    const bytes = fromBase64(base64);
    // Write without response: the AIREC command characteristic is write-no-response.
    if (state.command.writeValueWithoutResponse) await state.command.writeValueWithoutResponse(bytes);
    else await state.command.writeValue(bytes);
    return { ok: true };
  } catch (err) {
    return failure(err);
  }
}

function concat(chunks, length) {
  const all = new Uint8Array(length);
  let at = 0;
  for (const chunk of chunks) {
    all.set(chunk, at);
    at += chunk.length;
  }
  return all;
}

// poll returns the reply bytes received since replyCursor, the total file bytes so far, and
// whether the recorder disconnected.
function poll(replyCursor) {
  const state = link;
  if (!state) return { ok: false, disconnected: true, reply: '', replyLength: 0, fileLength: 0 };
  const all = concat(state.replyBytes, state.replyLength);
  const fresh = replyCursor < all.length ? all.subarray(replyCursor) : new Uint8Array(0);
  return {
    ok: true,
    disconnected: state.disconnected,
    reply: toBase64(fresh),
    replyLength: state.replyLength,
    fileLength: state.fileLength,
    liveLength: state.liveLength,
  };
}

function takeFileData() {
  const state = link;
  if (!state) return { ok: false, data: '' };
  const bytes = concat(state.fileChunks, state.fileLength);
  state.fileChunks = [];
  state.fileLength = 0;
  return { ok: true, data: toBase64(bytes) };
}

function clear() {
  const state = link;
  if (state) {
    state.replyBytes = [];
    state.replyLength = 0;
    state.fileChunks = [];
    state.fileLength = 0;
    state.liveLength = 0;
  }
  return { ok: true };
}

function close() {
  try {
    link?.device.gatt.disconnect();
  } catch {
    // already gone
  }
  link = null;
  return { ok: true };
}

window.airecBluetooth = { open, send, poll, takeFileData, clear, close };
