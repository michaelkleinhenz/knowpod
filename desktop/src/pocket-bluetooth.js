// Talks to a Pocket recorder (heypocketai.com) over Bluetooth: switches its USB drive on, and
// opens sessions for the WiFi transfer (pocket-wifi.js). The recorder is a USB drive only
// until it's unplugged once; after that it has to be told over Bluetooth to be one again, as
// the Pocket app does, before pocket.js can copy from it.
//
// The Bluetooth talk is in bluetooth.js, which runs in a hidden window for each call or
// session (Web Bluetooth needs a page); here the window is opened, the recorder picked when
// the page asks for a device, and the settings kept: the recorder's Bluetooth address and its
// session key, which unlocks the connection. The key is a secret (its first 8 characters are also the
// recorder's WiFi password), so it's encrypted with the system's keychain where there is one,
// and never handed to a page.
const { BrowserWindow, safeStorage } = require('electron');
const path = require('node:path');

// How long to look for the recorder before giving up (it may be asleep).
const scanTimeout = 20_000;
// How long a whole call may take, and in a session how long connecting may take.
const callTimeout = 60_000;
// How long one step of a session may take beyond its own timeout.
const stepSlack = 15_000;

const hex = (text) => String(text || '').replace(/[^0-9a-f]/gi, '').toUpperCase();

// normalizeAddress makes "aa-bb-cc-dd-ee-ff" or "aabbccddeeff" into "AA:BB:CC:DD:EE:FF", or
// returns null if it isn't a Bluetooth address.
function normalizeAddress(input) {
  const text = String(input || '').trim();
  if (!/^[0-9a-f]{2}([:-]?[0-9a-f]{2}){5}$/i.test(text)) return null;
  return hex(text).match(/../g).join(':');
}

// A session key is 16 characters; "&" would end the command it's sent in.
const validSessionKey = (key) => /^[\x21-\x25\x27-\x7e]{8,64}$/.test(key);

// pickDevice finds the recorder among the devices found so far. Windows and Linux give each
// device's real address. macOS hides addresses (it gives a made-up one), so there the
// recorder is the only device found whose name is a Pocket's ("PKT01_GREY_…").
function pickDevice(devices, address) {
  const wanted = hex(address);
  const byAddress = devices.find((d) => hex(d.deviceId) === wanted);
  if (byAddress || process.platform !== 'darwin') return byAddress;
  const pockets = devices.filter((d) => /^PKT01/i.test(d.deviceName || ''));
  return pockets.length === 1 ? pockets[0] : undefined;
}

// openWindow opens the hidden Bluetooth page and picks the recorder at address when the page
// asks for a device. Returns {exec(code), found(), close()}; exec runs code in the page as a
// user gesture (Web Bluetooth asks for a device only on one).
async function openWindow(address) {
  const win = new BrowserWindow({
    show: false,
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  let found = false; // the recorder was found
  let answered = false; // the page's request for a device was answered
  let gaveUp = false; // the scan took too long
  let cancel = null;
  // Called again and again while devices are found; the request waits until the callback of
  // one of them is called, with a device or with '' (none).
  win.webContents.on('select-bluetooth-device', (event, devices, callback) => {
    event.preventDefault();
    if (answered) return;
    const device = gaveUp ? undefined : pickDevice(devices, address);
    if (device) found = true;
    if (device || gaveUp) {
      answered = true;
      callback(device ? device.deviceId : '');
    } else {
      cancel = () => callback('');
    }
  });
  const scanTimer = setTimeout(() => {
    gaveUp = true;
    if (answered || !cancel) return;
    answered = true;
    cancel();
  }, scanTimeout);
  try {
    await win.loadFile(path.join(__dirname, 'bluetooth.html'));
  } catch (err) {
    clearTimeout(scanTimer);
    win.destroy();
    throw err;
  }
  return {
    exec: (code) => win.webContents.executeJavaScript(code, true),
    found: () => found,
    close: () => {
      clearTimeout(scanTimer);
      // Closing the page also drops the Bluetooth connection.
      if (!win.isDestroyed()) win.destroy();
    },
  };
}

// withTimeout resolves to promise, or to {ok: false, error} after ms.
function withTimeout(promise, ms, error) {
  let timer;
  const timeout = new Promise((resolve) => {
    timer = setTimeout(() => resolve({ ok: false, error }), ms);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

// runInWindow does action ('check' or 'usb-on', see bluetooth.js) on the recorder.
async function runInWindow(address, sessionKey, action) {
  let page = null;
  try {
    page = await openWindow(address);
    const call = page.exec(`window.pocketBluetooth.run(${JSON.stringify(action)}, ${JSON.stringify(sessionKey)})`);
    const result = await withTimeout(call, callTimeout, 'timeout');
    if (!result.ok && result.error === 'timeout' && !page.found()) return { ok: false, error: 'not-found' };
    return result;
  } catch (err) {
    return { ok: false, error: 'failed', message: String(err?.message || err) };
  } finally {
    page?.close();
  }
}

// SessionError is a failed step of a session, with the code bluetooth.js gave.
class SessionError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

// openSession connects to the recorder at address and unlocks it, for a session of several
// steps (see bluetooth.js: call()). Returns the session's steps, each rejecting with a
// SessionError when it fails; close() must be called at the end.
async function openSession(address, sessionKey) {
  const page = await openWindow(address);
  const opened = await withTimeout(page.exec(`window.pocketBluetooth.open(${JSON.stringify(sessionKey)})`), callTimeout, 'timeout');
  if (!opened.ok) {
    page.close();
    const code = opened.error === 'timeout' && !page.found() ? 'not-found' : opened.error;
    throw new SessionError(code, opened.message);
  }
  let closed = false;
  // step runs one call in the page; timeout is the step's own (ms), if it has one.
  async function step(method, args = [], timeout = 0) {
    if (closed) throw new SessionError('disconnected', 'The session is closed');
    const code = `window.pocketBluetooth.call(${JSON.stringify(method)}, ${JSON.stringify(args)})`;
    const result = await withTimeout(page.exec(code), timeout + stepSlack, 'timeout');
    if (!result.ok) throw new SessionError(result.error, result.message);
    return result.value;
  }
  return {
    // command sends name and resolves to the value of the answer, rejecting without one.
    command: (name, answer, timeout = 5_000, accept = 'any') => step('command', [name, answer, timeout, accept], timeout),
    // send writes "APP&<name>" and resolves to the mark before it.
    send: (name) => step('send', [name]),
    // waitFor resolves to the value of the first answer to answer after since, or null.
    waitFor: (answer, since, timeout, accept = 'any') => step('waitFor', [answer, since, timeout, accept], timeout),
    mark: () => step('mark'),
    subscribeAudio: () => step('subscribeAudio'),
    listRecordings: () => step('listRecordings', [], 60_000),
    connected: () => step('connected'),
    close: async () => {
      if (closed) return;
      closed = true;
      try {
        await withTimeout(page.exec('window.pocketBluetooth.close()'), 5_000, 'timeout');
      } catch {
        // the page is going anyway
      }
      page.close();
    },
  };
}

// createPocketBluetooth keeps the recorder's Bluetooth settings in the app's config and talks
// to the recorder. options: readConfig(), writeConfig(config), onChange() (busy() changed).
function createPocketBluetooth({ readConfig, writeConfig, onChange }) {
  const stored = () => readConfig().pocketBluetooth || {};

  function sessionKey() {
    const { sessionKey: key } = stored();
    if (!key) return '';
    try {
      if (key.encrypted) return safeStorage.decryptString(Buffer.from(key.encrypted, 'base64'));
      return key.plain || '';
    } catch {
      return ''; // e.g. the keychain changed
    }
  }

  const encryptKey = (key) =>
    safeStorage.isEncryptionAvailable() ? { encrypted: safeStorage.encryptString(key).toString('base64') } : { plain: key };

  const configured = () => !!stored().address && !!sessionKey();

  let busy = false;
  // call does one action at a time.
  async function call(action) {
    const { address } = stored();
    const key = sessionKey();
    if (!address || !key) return { ok: false, error: 'not-configured' };
    if (busy) return { ok: false, error: 'busy' };
    busy = true;
    onChange();
    try {
      const result = await runInWindow(address, key, action);
      if (!result.ok) console.error(`pocket bluetooth ${action}:`, result.error, result.message || '');
      return result;
    } finally {
      busy = false;
      onChange();
    }
  }

  return {
    // settings is what the settings page shows: never the key itself.
    settings: () => ({ address: stored().address || '', sessionKeySet: !!sessionKey(), busy }),
    // save changes the settings: address '' and sessionKey '' remove them, a missing
    // sessionKey keeps it. Returns {ok, error, settings}.
    save: ({ address, sessionKey: key } = {}) => {
      const next = { ...stored() };
      if (address !== undefined) {
        const normalized = address === '' ? '' : normalizeAddress(address);
        if (normalized === null) return { ok: false, error: 'invalid-address' };
        next.address = normalized;
      }
      if (key !== undefined) {
        const text = String(key).trim();
        if (text && !validSessionKey(text)) return { ok: false, error: 'invalid-key' };
        next.sessionKey = text ? encryptKey(text) : undefined;
      }
      const config = readConfig();
      if (next.address || next.sessionKey) config.pocketBluetooth = next;
      else delete config.pocketBluetooth;
      writeConfig(config);
      onChange();
      return { ok: true };
    },
    configured,
    busy: () => busy,
    // check reads the battery, firmware, storage and USB state.
    check: () => call('check'),
    // usbOn makes the recorder a USB drive again.
    usbOn: () => call('usb-on'),
    // session opens a Bluetooth session for the WiFi transfer (see openSession), holding the
    // connection (busy) until its close(). Rejects with a SessionError (not-configured, busy,
    // not-found, auth, unsupported, …) when it can't.
    session: async () => {
      const { address } = stored();
      const key = sessionKey();
      if (!address || !key) throw new SessionError('not-configured');
      if (busy) throw new SessionError('busy');
      busy = true;
      onChange();
      const release = () => {
        busy = false;
        onChange();
      };
      try {
        const opened = await openSession(address, key);
        const close = opened.close;
        return {
          ...opened,
          close: async () => {
            try {
              await close();
            } finally {
              release();
            }
          },
        };
      } catch (err) {
        release();
        throw err instanceof SessionError ? err : new SessionError('failed', String(err?.message || err));
      }
    },
  };
}

module.exports = { createPocketBluetooth, normalizeAddress, SessionError };
