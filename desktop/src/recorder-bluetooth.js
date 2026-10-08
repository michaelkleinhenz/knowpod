// Takes recordings from a knowpod recorder (the ESP32 gadget, esp32/) over Bluetooth while the
// recorder has no Wi-Fi, and uploads them to the backend for it (recorder-relay.js; the protocol
// is in docs/ble-transfer.md). The recorder advertises only while uploads wait and Wi-Fi can't be
// reached, so looking for it every few minutes costs nothing otherwise.
//
// The Bluetooth talk is in recorder-ble.js, which runs in a hidden window per connection (Web
// Bluetooth needs a page); here the window is opened, the recorder picked when the page asks for
// a device, and the pairing answered. Pairing is asked for once from the settings page (frontend/
// src/components/RecorderBluetooth.tsx): the recorder shows a passkey, which the page asks for
// and hands back here ('pin'); on macOS the system asks for it itself. The paired recorder's
// device id and name are kept in the app's config.
const { BrowserWindow, session } = require('electron');
const path = require('node:path');
const { relayRecordings, RelayError } = require('./recorder-relay');

// The hidden windows' own session: its pairing handler and fetches stay apart from the app's.
const partition = 'knowpod-recorder-bluetooth';
// How often to look for the recorder.
const lookInterval = 2 * 60_000;
// How long to look for it before giving up (it advertises only while it waits for an upload).
const scanTimeout = 15_000;
// While pairing: how long to look, and how long to wait for more recorders once one was seen.
const pairScanTimeout = 30_000;
const pairSettle = 3_000;
// How long pairing may take (typing the passkey included).
const pairTimeout = 120_000;
// How long one call into the page may take beyond its own timeout.
const callSlack = 15_000;

const recorderName = /^knowpod-/i;

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

// startRecorderBluetooth looks for the paired recorder and relays its recordings. options:
//   serverUrl()                  the app's server, or null
//   readConfig(), writeConfig(config)
//   notify({title, body, url, tag})
//   onChange()                   called when state() changed
function startRecorderBluetooth({ serverUrl, readConfig, writeConfig, notify, onChange }) {
  const stored = () => readConfig().recorderBluetooth || {};
  const save = (next) => {
    const config = readConfig();
    if (next) config.recorderBluetooth = next;
    else delete config.recorderBluetooth;
    writeConfig(config);
  };
  const paired = () => !!stored().name;
  const enabled = () => stored().enabled !== false;

  // state is what the settings page and the tray show. phase: '' (idle), searching, choose
  // (several recorders found: devices), connecting, pin (waiting for the passkey), listing,
  // preparing, uploading, done or failed (error, message).
  let state = { phase: '', current: 0, total: 0, title: '', bytes: 0, totalBytes: 0, copied: 0, failed: 0, error: '', message: '', devices: [] };
  let busy = false;
  let cancelled = false;
  let pinAnswer = null; // the pairing handler's callback while it waits for the passkey
  let pairing = false;
  let page = null;

  const set = (next) => {
    state = { ...state, ...next };
    onChange();
  };

  const ses = session.fromPartition(partition);
  // Windows and Linux: Chromium asks the app for the passkey. macOS asks the user itself.
  if (typeof ses.setBluetoothPairingHandler === 'function') {
    ses.setBluetoothPairingHandler((details, callback) => {
      // Only pairing the user asked for; a recorder that wants a new pairing otherwise is refused.
      if (!pairing) return callback({ confirmed: false });
      if (details.pairingKind === 'providePin') {
        pinAnswer = callback;
        set({ phase: 'pin' });
      } else {
        callback({ confirmed: details.pairingKind === 'confirm' });
      }
    });
  }

  // openWindow opens the hidden Bluetooth page; pick(devices) chooses the recorder when the page
  // asks for a device: a device, null (keep looking) or false (give up).
  async function openWindow(pick, timeout) {
    const win = new BrowserWindow({
      show: false,
      webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, partition },
    });
    let chosen = null;
    let answered = false;
    // Called again and again while devices are found; the request waits until the callback of
    // one of them is called, with a device or with '' (none).
    let pending = null;
    let seen = [];
    const answer = (device, callback) => {
      answered = true;
      chosen = device || null;
      callback(device ? device.deviceId : '');
    };
    win.webContents.on('select-bluetooth-device', (event, devices, callback) => {
      event.preventDefault();
      if (answered) return;
      seen = devices;
      const device = pick(devices, false);
      if (device || device === false) answer(device, callback);
      else pending = callback;
    });
    // Devices come in as they are found: pick may wait for more (pairing), so it's asked again
    // now and then, and a last time when the scan times out.
    const recheck = setInterval(() => {
      if (answered || !pending) return;
      const device = pick(seen, false);
      if (device || device === false) answer(device, pending);
    }, 500);
    const timer = setTimeout(() => {
      clearInterval(recheck);
      if (answered || !pending) return;
      answer(pick(seen, true), pending);
    }, timeout);
    try {
      await win.loadFile(path.join(__dirname, 'recorder-ble.html'));
    } catch (err) {
      clearTimeout(timer);
      clearInterval(recheck);
      win.destroy();
      throw err;
    }
    const exec = (code) => win.webContents.executeJavaScript(code, true);
    return {
      chosen: () => chosen,
      // open connects to the picked recorder; rejects with a LinkError.
      open: async () => {
        let r;
        try {
          r = await withTimeout(exec('window.recorderBluetooth.open()'), timeout + 30_000, 'timeout');
        } catch (err) {
          // Nothing found: the page's request never got a device.
          if (err.code === 'timeout' && !chosen) throw new LinkError('not-found', 'The recorder wasn’t found');
          throw err;
        }
        if (!r.ok) throw new LinkError(r.error, r.message);
        return r;
      },
      // link is the connection as recorder-relay.js uses it.
      link: {
        request: async (message, ms = 15_000) => {
          const r = await withTimeout(exec(`window.recorderBluetooth.request(${JSON.stringify(message)}, ${ms})`), ms + callSlack, 'timeout');
          if (!r.ok) throw new LinkError(r.error, r.message);
          return r.response;
        },
        read: async (id, offset, length) => {
          const args = [id, offset, length].map((a) => JSON.stringify(a)).join(', ');
          const r = await withTimeout(exec(`window.recorderBluetooth.read(${args})`), 60_000 + callSlack, 'timeout');
          if (!r.ok) throw new LinkError(r.error, r.message);
          return Buffer.from(r.data, 'base64');
        },
      },
      close: async () => {
        clearTimeout(timer);
        clearInterval(recheck);
        if (win.isDestroyed()) return;
        try {
          await withTimeout(exec('window.recorderBluetooth.close()'), 5_000, 'timeout');
        } catch {
          // the page goes anyway
        }
        if (!win.isDestroyed()) win.destroy();
      },
    };
  }

  // relay uploads what the connected recorder offers and tells how it went.
  async function relay(name, { quiet }) {
    const server = serverUrl();
    if (!server) throw new RelayError('no-server', 'No server set up');
    const result = await relayRecordings({
      link: page.link,
      serverUrl: server,
      fetch: (url, init) => ses.fetch(url, init),
      isCancelled: () => cancelled,
      onProgress: (p) => set({ phase: p.phase, current: p.current || 0, total: p.total || 0, title: p.title || '', bytes: p.bytes || 0, totalBytes: p.totalBytes || 0 }),
    });
    set({ phase: 'done', copied: result.copied, failed: result.failed, error: '', message: result.errors.map((e) => e.message).join('; ') });
    if (result.copied) {
      notify({
        title: `Copied from ${name}`,
        body: `${result.copied === 1 ? '1 recording was' : `${result.copied} recordings were`} uploaded over Bluetooth and will be transcribed and summarized.`,
        url: '/',
        tag: 'recorder-bluetooth',
      });
    } else if (result.failed && !quiet) {
      notify({ title: `Couldn’t copy from ${name}`, body: state.message, url: '/', tag: 'recorder-bluetooth' });
    }
  }

  const fail = (err, quiet) => {
    const code = err?.code || 'failed';
    console.error('recorder bluetooth:', code, err?.message || err);
    set({ phase: quiet && code === 'not-found' ? '' : 'failed', error: code, message: String(err?.message || err) });
  };

  // look connects to the paired recorder, if it advertises, and relays its recordings. Quiet
  // (the timer): not finding it is not worth telling.
  async function look({ quiet = false } = {}) {
    if (!paired() || !enabled() || busy || !serverUrl()) return;
    busy = true;
    cancelled = false;
    const { deviceId, name } = stored();
    if (!quiet) set({ phase: 'searching', error: '', message: '', copied: 0, failed: 0 });
    try {
      page = await openWindow((devices) => devices.find((d) => d.deviceId === deviceId) || devices.find((d) => d.deviceName === name) || null, scanTimeout);
      await page.open();
      set({ phase: 'connecting', error: '', message: '' });
      await relay(name, { quiet });
    } catch (err) {
      fail(err, quiet);
    } finally {
      busy = false;
      await page?.close();
      page = null;
      onChange();
    }
  }

  // pair finds a recorder in pairing mode (by name, if several are around), pairs with it and
  // relays what it offers right away.
  async function pair(wanted) {
    if (busy) return { ok: false, error: 'busy' };
    busy = true;
    pairing = true;
    cancelled = false;
    set({ phase: 'searching', error: '', message: '', devices: [], copied: 0, failed: 0 });
    void (async () => {
      let seenAt = 0;
      try {
        page = await openWindow((devices, final) => {
          const recorders = devices.filter((d) => recorderName.test(d.deviceName || ''));
          if (wanted) return recorders.find((d) => d.deviceName === wanted) || (final ? false : null);
          if (recorders.length && !seenAt) seenAt = Date.now();
          // Wait a moment for others: with several, the user picks one.
          if (!final && (!recorders.length || Date.now() - seenAt < pairSettle)) return null;
          if (recorders.length === 1) return recorders[0];
          if (recorders.length > 1) set({ devices: recorders.map((d) => d.deviceName) });
          return false;
        }, pairScanTimeout);
        const opened = await page.open();
        const device = page.chosen();
        set({ phase: 'connecting' });
        // The first request needs the paired link: the system pairs now.
        const info = await page.link.request({ op: 'info' }, pairTimeout);
        if (!info?.ok) throw new LinkError('failed', info?.message || 'The recorder did not answer');
        const name = info.name || opened.name || device?.deviceName || 'knowpod recorder';
        save({ ...stored(), deviceId: device?.deviceId || '', name, enabled: true });
        pairing = false;
        notify({ title: `Paired with ${name}`, body: 'Recordings it can’t upload over Wi-Fi now come over Bluetooth.', tag: 'recorder-bluetooth' });
        await relay(name, { quiet: false });
      } catch (err) {
        if (err?.code === 'not-found' && state.devices.length > 1) set({ phase: 'choose', error: '', message: '' });
        else fail(err, false);
      } finally {
        pairing = false;
        if (pinAnswer) pinAnswer({ confirmed: false });
        pinAnswer = null;
        busy = false;
        await page?.close();
        page = null;
        onChange();
      }
    })();
    return { ok: true };
  }

  setInterval(() => void look({ quiet: true }), lookInterval);
  setTimeout(() => void look({ quiet: true }), 10_000);

  return {
    // state is what the settings page shows.
    state: () => ({
      ok: true,
      supported: true,
      paired: paired(),
      name: stored().name || '',
      enabled: enabled(),
      busy,
      ...state,
    }),
    pair: (name) => pair(typeof name === 'string' && name ? name : ''),
    // pin answers the pairing with the passkey the recorder shows.
    pin: (pin) => {
      const text = String(pin || '').trim();
      if (!pinAnswer || !/^\d{6}$/.test(text)) return { ok: false, error: 'invalid-pin' };
      pinAnswer({ confirmed: true, pin: text });
      pinAnswer = null;
      set({ phase: 'connecting' });
      return { ok: true };
    },
    syncNow: () => {
      if (!paired()) return { ok: false, error: 'not-paired' };
      if (busy) return { ok: false, error: 'busy' };
      void look();
      return { ok: true };
    },
    cancel: () => {
      cancelled = true;
      if (pinAnswer) pinAnswer({ confirmed: false });
      pinAnswer = null;
      void page?.close();
      return { ok: true };
    },
    setEnabled: (on) => {
      if (!paired()) return { ok: false, error: 'not-paired' };
      save({ ...stored(), enabled: !!on });
      onChange();
      return { ok: true };
    },
    // forget drops the pairing here; the system's bond stays until removed in its settings.
    forget: () => {
      save(null);
      set({ phase: '', error: '', message: '', devices: [] });
      return { ok: true };
    },
  };
}

module.exports = { startRecorderBluetooth };
