// Switches the USB drive of a Pocket recorder (heypocketai.com) on over Bluetooth. The
// recorder is a USB drive only until it's unplugged once; after that it has to be told over
// Bluetooth to be one again, as the Pocket app does, before pocket.js can copy from it.
//
// The Bluetooth talk is in bluetooth.js, which runs in a hidden window for each call (Web
// Bluetooth needs a page); here the window is opened, the recorder picked when the page asks
// for a device, and the settings kept: the recorder's Bluetooth address and its session key,
// which unlocks the connection. The key is a secret (its first 8 characters are also the
// recorder's WiFi password), so it's encrypted with the system's keychain where there is one,
// and never handed to a page.
const { BrowserWindow, safeStorage } = require('electron');
const path = require('node:path');

// How long to look for the recorder before giving up (it may be asleep).
const scanTimeout = 20_000;
// How long a whole call may take.
const callTimeout = 60_000;

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

// runInWindow does action ('check' or 'usb-on', see bluetooth.js) on the recorder.
async function runInWindow(address, sessionKey, action) {
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
  let callTimer;
  try {
    await win.loadFile(path.join(__dirname, 'bluetooth.html'));
    const call = win.webContents.executeJavaScript(
      `window.pocketBluetooth.run(${JSON.stringify(action)}, ${JSON.stringify(sessionKey)})`,
      true, // a user gesture: Web Bluetooth asks for a device only on one
    );
    const timeout = new Promise((resolve) => {
      callTimer = setTimeout(() => resolve({ ok: false, error: found ? 'timeout' : 'not-found' }), callTimeout);
    });
    return await Promise.race([call, timeout]);
  } catch (err) {
    return { ok: false, error: 'failed', message: String(err?.message || err) };
  } finally {
    clearTimeout(scanTimer);
    clearTimeout(callTimer);
    // Closing the page also drops the Bluetooth connection.
    win.destroy();
  }
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
  };
}

module.exports = { createPocketBluetooth, normalizeAddress };
