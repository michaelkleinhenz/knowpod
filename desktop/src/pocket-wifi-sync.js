// Copies new recordings from a Pocket recorder over its WiFi, as the alternative to USB
// (pocket.js) in the Pocket Sync dialog (frontend/src/components/PocketUsbSync.tsx). While
// this machine is on the recorder's network it has no internet, so the order is:
//
//   1. Bluetooth: connect, check firmware (1.8) and battery, list the recordings.
//   2. Ask the server which of them aren't notes yet (still online).
//   3. Raise the recorder's access point, join it and download those (pocket-wifi.js) into a
//      temporary folder; then lower it and rejoin the usual network.
//   4. Upload them like the USB copy does; the server puts them into the folder "Pocket AI".
//
// state() is what the dialog shows: phase is '' (never run), connecting, listing, checking,
// wifi-starting, downloading (file current of total, bytes of totalBytes at rate bytes/s),
// wifi-restarting, reconnecting, uploading (current of total), done (copied, failed; found
// recordings on the recorder, incomplete if its listing came back short) or failed (error,
// message).
'use strict';

const { app } = require('electron');
const fs = require('node:fs');
const path = require('node:path');

const { request, HttpError, rememberedFiles, rememberFile } = require('./pocket');
const { createWifiSession, wifiSupported, WifiError } = require('./pocket-wifi');

// Bytes per second of a recording (32 kbps MP3), to estimate sizes before the transfer.
const bytesPerSecond = 4_000;
// After switching back, how long the server may take to be reachable again.
const reconnectTimeout = 90_000;

// SyncError carries a code for the dialog to explain: firmware, battery, no-server,
// signed-out, offline (see also the codes of pocket-bluetooth.js and pocket-wifi.js).
class SyncError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// startPocketWifiSync returns {state(), start(), cancel()}. options:
//   serverUrl(), readConfig(), writeConfig(config), notify({title, body, url, tag}),
//   pocketBluetooth (pocket-bluetooth.js), onChange() (state() changed)
function startPocketWifiSync({ serverUrl, readConfig, writeConfig, notify, pocketBluetooth, onChange }) {
  const empty = { phase: '', current: 0, total: 0, bytes: 0, totalBytes: 0, rate: 0, copied: 0, failed: 0, found: 0, incomplete: false, error: '', message: '' };
  let progress = { ...empty };
  let running = false;
  let cancelled = false;
  let session = null; // the WiFi session, while one is open

  const set = (next) => {
    progress = { ...progress, ...next };
    onChange();
  };
  const check = () => {
    if (cancelled) throw new WifiError('cancelled');
  };
  const log = (text) => console.log(`pocket wifi: ${text}`);

  // upload sends one file to the server, waiting for it to be reachable again after the WiFi
  // switch. Resolves to the note's id (or '' if it already was one).
  async function upload(server, file, name) {
    const deadline = Date.now() + reconnectTimeout;
    for (;;) {
      try {
        const rec = await request('POST', `${server}/api/v1/me/pocket/device/files`, {
          headers: { 'Content-Type': 'audio/mpeg', 'X-Filename': encodeURIComponent(name) },
          file,
        });
        return rec.id || '';
      } catch (err) {
        if (err instanceof HttpError) {
          if (err.status === 409) return ''; // already a note
          if (err.status === 401) throw new SyncError('signed-out', err.message);
          throw err;
        }
        // Not reachable yet: the usual network is still coming back.
        if (Date.now() >= deadline) throw new SyncError('offline', err.message);
        await sleep(3_000);
      }
    }
  }

  async function run() {
    const tempDir = path.join(app.getPath('temp'), 'knowpod-pocket-wifi');
    let ble = null;
    let copied = 0;
    let failed = 0;
    let lastFailure = null;
    let lastId = '';
    try {
      if (!wifiSupported()) throw new WifiError('wifi-unsupported');
      const server = serverUrl();
      if (!server) throw new SyncError('no-server');

      set({ phase: 'connecting' });
      ble = await pocketBluetooth.session();
      check();
      const firmware = (await ble.command('FW', 'FW')).trim();
      if (!firmware.startsWith('1.8')) throw new SyncError('firmware', firmware);
      const battery = parseInt(await ble.command('BAT', 'BAT'), 10);
      if (Number.isFinite(battery) && battery < 10) throw new SyncError('battery', String(battery));

      set({ phase: 'listing' });
      const known = rememberedFiles(readConfig, server);
      const listing = await ble.listRecordings();
      const listed = listing.recordings.map((r) => ({ ...r, name: `${r.timestamp}.mp3` }));
      const files = listed.filter((f) => !known.has(f.name));
      set({ found: listed.length, incomplete: listing.incomplete });
      log(
        `${listed.length} recordings on ${listing.days} days${listing.incomplete ? ' (listing incomplete)' : ''}, ` +
          `${listed.length - files.length} copied before: ${listed.map((f) => f.name).join(' ')}`,
      );
      check();

      set({ phase: 'checking' });
      let fresh = [];
      if (files.length) {
        try {
          ({ files: fresh = [] } = await request('POST', `${server}/api/v1/me/pocket/device/check`, {
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ files: files.map((f) => f.name) }),
          }));
        } catch (err) {
          if (err instanceof HttpError && err.status === 401) throw new SyncError('signed-out', err.message);
          throw err instanceof HttpError ? err : new SyncError('offline', err.message);
        }
      }
      const wanted = new Set(fresh);
      for (const f of files) if (!wanted.has(f.name)) rememberFile(readConfig, writeConfig, server, f.name);
      const todo = files.filter((f) => wanted.has(f.name));
      log(`${files.length - todo.length} already notes, ${todo.length} new`);
      if (!todo.length) {
        set({ phase: 'done', copied: 0, failed: 0 });
        return;
      }
      check();

      set({ phase: 'wifi-starting', current: 0, total: todo.length });
      fs.mkdirSync(tempDir, { recursive: true });
      session = createWifiSession(ble, {
        log,
        onRestart: () => set({ phase: 'wifi-restarting' }),
      });
      await session.start();

      const downloaded = [];
      for (const [i, f] of todo.entries()) {
        check();
        const file = path.join(tempDir, f.name);
        const started = Date.now();
        set({ phase: 'downloading', current: i + 1, total: todo.length, bytes: 0, totalBytes: f.seconds * bytesPerSecond, rate: 0 });
        try {
          await session.download(f, file, (bytes, size) => {
            const seconds = (Date.now() - started) / 1000;
            set({ phase: 'downloading', bytes, totalBytes: size, rate: seconds > 0.5 ? bytes / seconds : 0 });
          });
          downloaded.push({ ...f, file });
        } catch (err) {
          if (err instanceof WifiError && err.code === 'cancelled') throw err;
          // The Bluetooth connection is gone, or the recorder stopped answering WiFi commands:
          // nothing more can be transferred.
          if (err && ['disconnected', 'timeout', 'stuck'].includes(err.code)) throw err;
          failed++;
          lastFailure = err;
          console.error(`pocket wifi: ${f.name}:`, err);
        }
      }

      set({ phase: 'reconnecting' });
      await session.close();
      session = null;
      await ble.close();
      ble = null;

      for (const [i, f] of downloaded.entries()) {
        check();
        set({ phase: 'uploading', current: i + 1, total: downloaded.length });
        lastId = (await upload(server, f.file, f.name)) || lastId;
        rememberFile(readConfig, writeConfig, server, f.name);
        copied++;
        fs.rmSync(f.file, { force: true });
      }
      set({
        phase: 'done',
        copied,
        failed,
        error: failed ? lastFailure?.code || 'transfer' : '',
        message: failed ? String(lastFailure?.message || '') : '',
      });
      if (copied)
        notify({
          title: 'Copied from Pocket',
          body: `${copied === 1 ? '1 new recording is' : `${copied} new recordings are`} in the folder "Pocket AI" and will be transcribed and summarized.`,
          url: copied === 1 && lastId ? `/conversations/${lastId}` : '/',
          tag: 'pocket-sync',
        });
    } catch (err) {
      const code = err?.code || (err instanceof HttpError ? 'server' : 'failed');
      set({ phase: 'failed', error: code, message: String(err?.message || err), copied, failed });
      if (code !== 'cancelled') {
        console.error('pocket wifi sync:', err);
        notify({
          title: "Couldn't copy from Pocket over WiFi",
          body: `${copied ? `${copied} copied. ` : ''}${String(err?.message || err)}`,
          url: '/',
          tag: 'pocket-sync',
        });
      }
    } finally {
      // Always: lower the access point, put the WiFi back, end the Bluetooth session, and
      // don't leave recordings lying around in the temporary folder.
      await session?.close();
      session = null;
      await ble?.close();
      fs.rmSync(tempDir, { recursive: true, force: true });
      running = false;
      onChange();
    }
  }

  return {
    supported: wifiSupported,
    state: () => ({ running, cancelling: running && cancelled, ...progress }),
    // start runs a copy unless one runs already.
    start: () => {
      if (running) return false;
      running = true;
      cancelled = false;
      progress = { ...empty, phase: 'connecting' };
      onChange();
      void run();
      return true;
    },
    // cancel stops the copy that runs; the WiFi and the recorder are put back first.
    cancel: () => {
      if (!running) return;
      cancelled = true;
      session?.cancel();
      onChange();
    },
  };
}

module.exports = { startPocketWifiSync };
