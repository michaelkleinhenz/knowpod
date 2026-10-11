// Opens a Bluetooth LE link to a Pocket recorder through BlueZ, on Linux, before Web Bluetooth
// connects to it.
//
// The recorder's advertising doesn't say "BR/EDR not supported", so BlueZ takes it for a
// dual-mode device, and the plain Device1.Connect that Chromium's Web Bluetooth uses pages it
// over classic Bluetooth. The recorder never answers that, and the connect fails after the page
// timeout. Adapter1.ConnectDevice connects over LE only. Once the recorder is connected that
// way, Chromium offers it to requestDevice as a connected device and gatt.connect() uses the
// open link instead of connecting again. Found with tools/pocket-wifi-probe (pocket.py's
// _connect_le, which has the details).
//
// ConnectDevice is part of BlueZ's experimental API: it needs "Experimental = true" in
// /etc/bluetooth/main.conf. Without it, or when the LE connect fails, Web Bluetooth's own
// connect runs as before.
'use strict';

const { execFile } = require('node:child_process');

// How long ConnectDevice may take; the recorder answers in 1-3 s when it's awake and free.
const connectTimeout = 15_000;

// busctl runs busctl on the system bus and resolves to {ok, out, err}; it never rejects.
function busctl(args, timeout) {
  return new Promise((resolve) => {
    execFile('busctl', ['--system', `--timeout=${Math.ceil(timeout / 1000)}`, ...args], { timeout: timeout + 2_000 }, (err, stdout, stderr) => {
      resolve({ ok: !err, out: String(stdout || ''), err: String(stderr || (err && err.message) || '').trim() });
    });
  });
}

// devicePath is BlueZ's object path of the device at address on adapter.
const devicePath = (adapter, address) => `${adapter}/dev_${address.toUpperCase().replace(/:/g, '_')}`;

// connectArgs are busctl's arguments for Adapter1.ConnectDevice. AddressType is the device's
// own kind of address (the recorder's is public), not the bearer: ConnectDevice is LE-only.
const connectArgs = (adapter, address) => [
  'call', 'org.bluez', adapter, 'org.bluez.Adapter1', 'ConnectDevice', 'a{sv}', '2',
  'Address', 's', address.toUpperCase(), 'AddressType', 's', 'public',
];

// connectLe connects the recorder at address over LE. Resolves to true when it's connected,
// false otherwise (log says why); never rejects. Linux only.
async function connectLe(address, log = () => undefined, adapter = '/org/bluez/hci0') {
  // ConnectDevice refuses a device BlueZ already knows (e.g. from an earlier scan), so forget
  // it first. Fails harmlessly when BlueZ doesn't know it.
  await busctl(['call', 'org.bluez', adapter, 'org.bluez.Adapter1', 'RemoveDevice', 'o', devicePath(adapter, address)], 5_000);
  const r = await busctl(connectArgs(adapter, address), connectTimeout);
  if (r.ok) {
    log(`connected to ${address} over LE`);
    return true;
  }
  if (/UnknownMethod|Unknown method/i.test(r.err)) {
    log('BlueZ has no ConnectDevice; set "Experimental = true" in /etc/bluetooth/main.conf and restart bluetooth');
  } else {
    log(`LE connect to ${address} failed: ${r.err || 'no answer'}`);
  }
  return false;
}

module.exports = { connectLe, connectArgs, devicePath };
