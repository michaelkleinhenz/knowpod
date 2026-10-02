// Talks to a Pocket recorder (heypocketai.com) over Bluetooth LE, with Web Bluetooth. Runs in
// a hidden window of its own (bluetooth.html), which pocket-bluetooth.js opens for one call of
// pocketBluetooth.run() and closes afterwards; the main process picks the recorder when the
// page asks for a device (select-bluetooth-device), so no device chooser is shown.
//
// The recorder speaks ASCII: the app writes "APP&<command>" to a characteristic, and the
// recorder answers "MCU&<command>&<value>" in notifications of the same characteristic. A
// connection has to be unlocked with the recorder's session key first ("APP&SK&<key>",
// answered "MCU&SK&OK"). Decoded by pocket-libre (github.com/shahcolate/pocket-libre, see its
// PROTOCOL.md); the commands used here:
//   BAT      → MCU&BAT&58              battery, percent
//   FW       → MCU&FW&1.3.3            firmware version
//   SPACE    → MCU&SPA&060846&061032   storage used and total, KB
//   GET&USB  → MCU&USB&1               whether the recorder is a USB drive when plugged in
//   USB&1    → MCU&USB&1               be one (the recorder switches it off when unplugged)
'use strict';

const SERVICE = '001120a0-2233-4455-6677-889912345678';
const COMMAND = '001120a3-2233-4455-6677-889912345678';

// How long to wait for an answer.
const answerTimeout = 5_000;

// BluetoothError carries a code for the app to explain (see pocket-bluetooth.js).
class BluetoothError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

// connect opens a connection to the recorder the main process picks and returns
// {command(name, answer), close()}. command writes "APP&<name>" and resolves to the value of
// the first answer "MCU&<answer>&<value>".
async function connect() {
  if (!navigator.bluetooth) throw new BluetoothError('unsupported', 'Web Bluetooth is not available');
  let device;
  try {
    device = await navigator.bluetooth.requestDevice({ acceptAllDevices: true, optionalServices: [SERVICE] });
  } catch (err) {
    // The main process cancels the request when the recorder wasn't found in time.
    if (err && err.name === 'NotFoundError') throw new BluetoothError('not-found', err.message);
    throw err;
  }
  const server = await device.gatt.connect();
  const characteristic = await (await server.getPrimaryService(SERVICE)).getCharacteristic(COMMAND);

  // waiting is the answer being waited for: {prefix, resolve, reject}.
  let waiting = null;
  characteristic.addEventListener('characteristicvaluechanged', (event) => {
    const text = new TextDecoder('ascii').decode(event.target.value).replace(/\0+$/, '').trim();
    if (waiting && text.startsWith(waiting.prefix)) {
      const { resolve } = waiting;
      waiting = null;
      resolve(text);
    }
  });
  device.addEventListener('gattserverdisconnected', () => {
    if (!waiting) return;
    const { reject } = waiting;
    waiting = null;
    reject(new BluetoothError('disconnected', 'The recorder disconnected'));
  });
  await characteristic.startNotifications();

  const write = (bytes) =>
    characteristic.properties.writeWithoutResponse
      ? characteristic.writeValueWithoutResponse(bytes)
      : characteristic.writeValueWithResponse(bytes);

  async function command(name, answer) {
    const prefix = `MCU&${answer}&`;
    const answered = new Promise((resolve, reject) => {
      waiting = { prefix, resolve, reject };
    });
    const timer = setTimeout(() => {
      if (!waiting || waiting.prefix !== prefix) return;
      const { reject } = waiting;
      waiting = null;
      reject(new BluetoothError('no-answer', `No answer to ${name}`));
    }, answerTimeout);
    try {
      try {
        await write(new TextEncoder().encode(`APP&${name}`));
      } catch (err) {
        if (waiting?.prefix === prefix) waiting = null;
        throw err;
      }
      const text = await answered;
      return text.slice(prefix.length);
    } finally {
      clearTimeout(timer);
    }
  }

  return {
    command,
    close: () => {
      try {
        device.gatt.disconnect();
      } catch {
        // already gone
      }
    },
  };
}

// optional runs a command whose answer is nice to have, resolving to null without one.
async function optional(promise) {
  try {
    return await promise;
  } catch (err) {
    if (err instanceof BluetoothError && err.code === 'no-answer') return null;
    throw err;
  }
}

const usbState = (value) => (value === '1' ? true : value === '0' ? false : null);

// run connects to the recorder, unlocks it with the session key and does action:
//   'check'   reads the battery, firmware, storage and USB state
//   'usb-on'  makes the recorder a USB drive again
// It resolves to {ok: true, ...} or {ok: false, error: code, message}, never rejects.
async function run(action, sessionKey) {
  let pocket = null;
  try {
    pocket = await connect();
    if ((await optional(pocket.command(`SK&${sessionKey}`, 'SK'))) !== 'OK') {
      throw new BluetoothError('auth', 'The recorder refused the session key');
    }
    if (action === 'check') {
      const battery = parseInt(await pocket.command('BAT', 'BAT'), 10);
      const firmware = await optional(pocket.command('FW', 'FW'));
      const space = await optional(pocket.command('SPACE', 'SPA'));
      const usb = await optional(pocket.command('GET&USB', 'USB'));
      const [used, total] = (space || '').split('&').map((n) => parseInt(n, 10));
      return {
        ok: true,
        battery: Number.isFinite(battery) ? battery : null,
        firmware: firmware ? firmware.trim() : null,
        storage: Number.isFinite(used) && Number.isFinite(total) ? { usedKB: used, totalKB: total } : null,
        usb: usbState(usb),
      };
    }
    if (action === 'usb-on') {
      await optional(pocket.command('USB&1', 'USB'));
      const usb = usbState(await pocket.command('GET&USB', 'USB'));
      if (usb !== true) throw new BluetoothError('usb-refused', 'The recorder did not switch its USB drive on');
      return { ok: true, usb };
    }
    throw new BluetoothError('failed', `Unknown action ${action}`);
  } catch (err) {
    return { ok: false, error: err instanceof BluetoothError ? err.code : 'failed', message: String((err && err.message) || err) };
  } finally {
    pocket?.close();
  }
}

window.pocketBluetooth = { run };
