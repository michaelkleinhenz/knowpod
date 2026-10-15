// Talks to an AIREC recorder over Bluetooth for the "Recorder Sync" dialog's AIREC tab
// (frontend/src/components/RecorderSync.tsx): it connects, reads the recorder's details and its
// list of recordings, downloads a recording as an Ogg Opus file (packed in airec-protocol.js)
// for the web app to import, and deletes a recording. The protocol is in
// tools/pocket-wifi-probe/AIREC.md.
//
// The Bluetooth transport is in airec-ble.js, which runs in a hidden window (Web Bluetooth needs
// a page); here the window is opened and the recorder picked when the page asks for a device, so
// no chooser is shown. The recorder has no pairing, so there is nothing to pair or unlock.
'use strict';

const { BrowserWindow, session } = require('electron');
const path = require('node:path');
const proto = require('./airec-protocol');

const partition = 'knowpod-airec-bluetooth';
const scanTimeout = 15_000;
const pollInterval = 80;
const commandTimeout = 5_000;
const listTimeout = 12_000;
const downloadTimeout = 180_000;

class LinkError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

function withTimeout(promise, ms, code) {
  let timer;
  const timeout = new Promise((_resolve, reject) => {
    timer = setTimeout(() => reject(new LinkError(code, code)), ms);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// startAirecBluetooth returns the call surface used by main.js. options:
//   onChange()   called when connected/busy changes (so the tray could reflect it)
function startAirecBluetooth({ onChange = () => {} } = {}) {
  let page = null; // the open hidden-window connection
  let busy = false;
  let recorderName = '';

  const ses = session.fromPartition(partition);
  // The recorder has no pairing; refuse any pairing a device asks for.
  if (typeof ses.setBluetoothPairingHandler === 'function') {
    ses.setBluetoothPairingHandler((details, callback) => callback({ confirmed: false }));
  }

  // openWindow opens the hidden Bluetooth page and connects to the first AIREC device found.
  async function openWindow() {
    const win = new BrowserWindow({
      show: false,
      webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, partition },
    });
    let answered = false;
    let chosen = null;
    let pending = null;
    let seen = [];
    const pick = (devices) => devices.find((d) => proto.NAME_RE.test(d.deviceName || '')) || null;
    const answer = (device, callback) => {
      answered = true;
      chosen = device || null;
      callback(device ? device.deviceId : '');
    };
    win.webContents.on('select-bluetooth-device', (event, devices, callback) => {
      event.preventDefault();
      if (answered) return;
      seen = devices;
      const device = pick(devices);
      if (device) answer(device, callback);
      else pending = callback;
    });
    const recheck = setInterval(() => {
      if (answered || !pending) return;
      const device = pick(seen);
      if (device) answer(device, pending);
    }, 300);
    const timer = setTimeout(() => {
      clearInterval(recheck);
      if (!answered && pending) answer(null, pending); // give up: '' means none
    }, scanTimeout);

    try {
      await win.loadFile(path.join(__dirname, 'airec-ble.html'));
    } catch (err) {
      clearTimeout(timer);
      clearInterval(recheck);
      win.destroy();
      throw err;
    }
    const exec = (code) => win.webContents.executeJavaScript(code, true);
    const stop = () => {
      clearTimeout(timer);
      clearInterval(recheck);
    };
    return {
      chosen: () => chosen,
      open: async () => {
        let r;
        try {
          r = await withTimeout(exec('window.airecBluetooth.open()'), scanTimeout + 30_000, 'timeout');
        } catch (err) {
          stop();
          if (err.code === 'timeout' && !chosen) throw new LinkError('not-found', 'No AIREC recorder was found');
          throw err;
        }
        stop();
        if (!r.ok) throw new LinkError(r.error, r.message);
        return r;
      },
      exec,
      close: async () => {
        stop();
        if (win.isDestroyed()) return;
        try {
          await withTimeout(exec('window.airecBluetooth.close()'), 5_000, 'timeout');
        } catch {
          // the window goes anyway
        }
        if (!win.isDestroyed()) win.destroy();
      },
    };
  }

  // collect sends a command, then polls the page, feeding a FrameParser, until done(frames)
  // returns true, or it times out. Returns the frames gathered.
  async function collect(cmd, data, done, timeout) {
    if (!page) throw new LinkError('disconnected', 'Not connected');
    await page.exec('window.airecBluetooth.clear()');
    const parser = new proto.FrameParser();
    const frames = [];
    await page.exec(`window.airecBluetooth.send(${JSON.stringify(toBase64(proto.build(cmd, data)))})`);
    let cursor = 0;
    const deadline = Date.now() + timeout;
    for (;;) {
      const r = await page.exec(`window.airecBluetooth.poll(${cursor})`);
      if (!r.ok || r.disconnected) throw new LinkError('disconnected', 'The recorder disconnected');
      cursor = r.replyLength;
      if (r.reply) for (const f of parser.feed(fromBase64(r.reply))) frames.push(f);
      if (done(frames, r)) return frames;
      if (Date.now() > deadline) throw new LinkError('timeout', `No answer to command 0x${cmd.toString(16)}`);
      await delay(pollInterval);
    }
  }

  const frameValue = (frames, cmd) => frames.find((f) => f.cmd === cmd)?.data;

  // askByte etc. read one value command; missing answers become undefined, so one quiet
  // recorder quirk doesn't fail the whole info read.
  async function askText(cmd) {
    const frames = await collect(cmd, undefined, (fs) => fs.some((f) => f.cmd === cmd), commandTimeout).catch(() => []);
    const v = frameValue(frames, cmd);
    return v ? String.fromCharCode(...v).replace(/\0+$/, '') : '';
  }

  async function readInfo() {
    const info = { name: recorderName };
    info.serial = await askText(proto.CMD.serial);
    info.firmware = await askText(proto.CMD.firmware);
    info.model = await askText(proto.CMD.model);
    const battery = await collect(proto.CMD.battery, undefined, (fs) => fs.some((f) => f.cmd === proto.CMD.battery), commandTimeout).catch(() => []);
    const b = frameValue(battery, proto.CMD.battery);
    info.battery = b && b.length ? b[0] : null;
    const storage = await collect(
      proto.CMD.storage,
      undefined,
      (fs) => fs.some((f) => f.cmd === proto.CMD.storageFree) && fs.some((f) => f.cmd === proto.CMD.storageTotal),
      commandTimeout,
    ).catch(() => []);
    const free = frameValue(storage, proto.CMD.storageFree);
    const total = frameValue(storage, proto.CMD.storageTotal);
    const u32 = (d) => (d && d.length >= 4 ? ((d[0] << 24) | (d[1] << 16) | (d[2] << 8) | d[3]) >>> 0 : null);
    info.storageFreeMb = u32(free);
    info.storageTotalMb = u32(total);
    return info;
  }

  async function readList() {
    const frames = await collect(proto.CMD.listEntry, undefined, (fs) => fs.some((f) => f.cmd === proto.CMD.listEnd), listTimeout);
    return frames
      .filter((f) => f.cmd === proto.CMD.listEntry)
      .map((f) => proto.parseEntry(f.data))
      .map((e) => proto.recordingMeta(e.name, e.size));
  }

  // connect opens the link and reads the recorder's details and recordings.
  async function connect() {
    if (busy) return { ok: false, error: 'busy' };
    if (page) await disconnect();
    busy = true;
    onChange();
    try {
      page = await openWindow();
      const opened = await page.open();
      recorderName = opened.name || page.chosen()?.deviceName || 'AIREC';
      const recorder = await readInfo();
      const recordings = await readList();
      return { ok: true, recorder, recordings };
    } catch (err) {
      await closePage();
      return fail(err);
    } finally {
      busy = false;
      onChange();
    }
  }

  async function refresh() {
    if (!page) return { ok: false, error: 'disconnected' };
    if (busy) return { ok: false, error: 'busy' };
    busy = true;
    try {
      return { ok: true, recordings: await readList() };
    } catch (err) {
      return fail(err);
    } finally {
      busy = false;
    }
  }

  // download fetches a recording and packs it as an Ogg Opus file for the web app to import.
  async function download(name) {
    if (!page) return { ok: false, error: 'disconnected' };
    if (!name) return { ok: false, error: 'failed' };
    if (busy) return { ok: false, error: 'busy' };
    busy = true;
    onChange();
    try {
      const nameBytes = new TextEncoder().encode(name);
      const req = new Uint8Array(nameBytes.length + 4); // name + 4-byte offset 0
      req.set(nameBytes);
      const header = await collect(
        proto.CMD.download,
        req,
        (fs) => fs.some((f) => f.cmd === proto.CMD.download || f.cmd === proto.CMD.error),
        commandTimeout,
      );
      if (frameValue(header, proto.CMD.error)) throw new LinkError('recorder', 'The recorder refused the download');
      const head = header.find((f) => f.cmd === proto.CMD.download);
      const size = head ? proto.parseEntry(head.data).size : 0;
      // The data arrives on the file characteristic; wait for the "done" frame, or until no more
      // data has arrived for a while.
      await waitForDone(size);
      const r = await page.exec('window.airecBluetooth.takeFileData()');
      const data = fromBase64(r.data || '');
      if (size && data.length < size) throw new LinkError('transfer', `Got ${data.length} of ${size} bytes`);
      const packed = proto.packRecordingAsOgg(data);
      const date = proto.nameToDate(name);
      return {
        ok: true,
        name,
        size: data.length,
        oggBase64: toBase64(packed.ogg),
        recordedAt: date ? date.toISOString() : null,
        resyncs: packed.resyncs.length,
      };
    } catch (err) {
      return fail(err);
    } finally {
      busy = false;
      onChange();
    }
  }

  async function waitForDone(size) {
    const parser = new proto.FrameParser();
    let cursor = 0;
    let lastLen = -1;
    let stillSince = Date.now();
    const deadline = Date.now() + downloadTimeout;
    for (;;) {
      const r = await page.exec(`window.airecBluetooth.poll(${cursor})`);
      if (!r.ok || r.disconnected) throw new LinkError('disconnected', 'The recorder disconnected');
      cursor = r.replyLength;
      let done = false;
      if (r.reply) for (const f of parser.feed(fromBase64(r.reply))) if (f.cmd === proto.CMD.downloadDone) done = true;
      if (done) return;
      if (r.fileLength !== lastLen) {
        lastLen = r.fileLength;
        stillSince = Date.now();
      } else if (size && r.fileLength >= size) {
        return; // all bytes came, even if the done frame was missed
      } else if (Date.now() - stillSince > commandTimeout) {
        return; // no more data and no done frame: take what came
      }
      if (Date.now() > deadline) throw new LinkError('timeout', 'The download stalled');
      await delay(pollInterval);
    }
  }

  // remove deletes a recording: the app sends 0x34 00 01, then 0x0a <name>.
  async function remove(name) {
    if (!page) return { ok: false, error: 'disconnected' };
    if (!name) return { ok: false, error: 'failed' };
    if (busy) return { ok: false, error: 'busy' };
    busy = true;
    onChange();
    try {
      await page.exec(`window.airecBluetooth.send(${JSON.stringify(toBase64(proto.build(proto.CMD.preDelete, new Uint8Array([0x00, 0x01]))))})`);
      await delay(200);
      const frames = await collect(
        proto.CMD.delete,
        new TextEncoder().encode(name),
        (fs) => fs.some((f) => f.cmd === proto.CMD.delete || f.cmd === proto.CMD.error),
        commandTimeout,
      );
      if (frameValue(frames, proto.CMD.error)) throw new LinkError('recorder', 'The recorder refused the delete');
      const recordings = await readList();
      return { ok: true, recordings };
    } catch (err) {
      return fail(err);
    } finally {
      busy = false;
      onChange();
    }
  }

  function fail(err) {
    const code = err?.code || 'failed';
    console.error('airec bluetooth:', code, err?.message || err);
    return { ok: false, error: code, message: String(err?.message || err) };
  }

  async function closePage() {
    const p = page;
    page = null;
    recorderName = '';
    if (p) await p.close().catch(() => undefined);
  }

  async function disconnect() {
    await closePage();
    onChange();
    return { ok: true };
  }

  return {
    connected: () => !!page,
    busy: () => busy,
    state: () => ({ ok: true, supported: true, connected: !!page, busy }),
    connect,
    refresh,
    download,
    delete: remove,
    disconnect,
  };
}

function toBase64(bytes) {
  return Buffer.from(bytes).toString('base64');
}

function fromBase64(text) {
  return new Uint8Array(Buffer.from(text, 'base64'));
}

module.exports = { startAirecBluetooth };
