// Talks to a Pocket recorder (heypocketai.com) over Bluetooth LE, with Web Bluetooth. Runs in
// a hidden window of its own (bluetooth.html), which pocket-bluetooth.js opens for one call of
// pocketBluetooth.run(), or for a longer session (open(), call(), close()) that the WiFi
// transfer (pocket-wifi.js) drives from the main process; the main process picks the recorder
// when the page asks for a device (select-bluetooth-device), so no device chooser is shown.
//
// The recorder speaks ASCII: the app writes "APP&<command>" to a characteristic, and the
// recorder answers "MCU&<command>&<value>" in notifications of the same characteristic. A
// connection has to be unlocked with the recorder's session key first ("APP&SK&<key>",
// answered "MCU&SK&OK"). Decoded by pocket-libre (github.com/shahcolate/pocket-libre, see its
// PROTOCOL.md); the commands used here:
//   BAT         → MCU&BAT&58              battery, percent
//   FW          → MCU&FW&1.8              firmware version
//   SPACE       → MCU&SPA&060846&061032   storage used and total, KB
//   GET&USB     → MCU&USB&1               whether the recorder is a USB drive when plugged in
//   USB&1       → MCU&USB&1               be one (the recorder switches it off when unplugged)
//   LIST_DIRS   → MCU&DIRS&<date>… MCU&DIRS_SUM&<n>                 days with recordings
//   LIST&<date> → MCU&F&<date>&<timestamp>&<seconds>… MCU&LIST&<n>   a day's recordings
// and for the WiFi transfer WIFIO, WIFI, WIFIS, WPING, U&<date>&<timestamp>, U&WIFI and WIFIC
// (see pocket-wifi.js).
'use strict';

const SERVICE = '001120a0-2233-4455-6677-889912345678';
const COMMAND = '001120a3-2233-4455-6677-889912345678';
// The recording data of a Bluetooth transfer. A transfer only runs while this is subscribed
// to, and the WiFi transfer switches a running Bluetooth transfer over, so a session
// subscribes and throws the data away.
const AUDIO = '001120a1-2233-4455-6677-889912345678';

// How long to wait for an answer.
const answerTimeout = 5_000;
// How long to wait for the end of a listing.
const listTimeout = 10_000;

// BluetoothError carries a code for the app to explain (see pocket-bluetooth.js).
class BluetoothError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

// splitMessages splits a notification that carries several answers ("MCU&WIFIOMCU&OFF").
const splitMessages = (text) =>
  text
    .replace(/\0/g, '')
    .split(/(?=MCU&)/)
    .map((part) => part.trim())
    .filter(Boolean);

// valueOf is the value of an answer "MCU&<answer>&<value>" ("" for a bare "MCU&<answer>"),
// or null if text is another answer.
function valueOf(text, answer) {
  const exact = `MCU&${answer}`;
  if (text === exact) return '';
  return text.startsWith(`${exact}&`) ? text.slice(exact.length + 1) : null;
}

// connect opens a connection to the recorder the main process picks. Every answer is kept in
// order, so answers that come on their own (WIFIS changes, MCU&OFF) can be waited for after a
// mark(): see waitFor.
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
  const service = await server.getPrimaryService(SERVICE);
  const characteristic = await service.getCharacteristic(COMMAND);

  const messages = [];
  const listeners = new Set();
  let disconnected = false;
  const changed = () => listeners.forEach((listener) => listener());
  characteristic.addEventListener('characteristicvaluechanged', (event) => {
    messages.push(...splitMessages(new TextDecoder('ascii').decode(event.target.value)));
    changed();
  });
  device.addEventListener('gattserverdisconnected', () => {
    disconnected = true;
    changed();
  });
  await characteristic.startNotifications();

  // Writes go one at a time: a heartbeat may be sent while another command waits.
  let writing = Promise.resolve();
  const write = (text) => {
    const bytes = new TextEncoder().encode(text);
    const next = writing.then(() =>
      characteristic.properties.writeWithoutResponse
        ? characteristic.writeValueWithoutResponse(bytes)
        : characteristic.writeValueWithResponse(bytes),
    );
    writing = next.catch(() => undefined);
    return next;
  };

  const mark = () => messages.length;

  // waitFor resolves to the value of the first answer to answer after since that accept()
  // takes, or to null after timeout ms; it rejects when the recorder disconnects.
  function waitFor(answer, since, timeout, accept = () => true) {
    return new Promise((resolve, reject) => {
      let index = since;
      let timer;
      const finish = (fn, value) => {
        listeners.delete(look);
        clearTimeout(timer);
        fn(value);
      };
      function look() {
        while (index < messages.length) {
          const value = valueOf(messages[index++], answer);
          if (value !== null && accept(value)) return finish(resolve, value);
        }
        if (disconnected) finish(reject, new BluetoothError('disconnected', 'The recorder disconnected'));
      }
      listeners.add(look);
      timer = setTimeout(() => finish(resolve, null), timeout);
      look();
    });
  }

  // send writes "APP&<name>" and returns the mark before it.
  async function send(name) {
    if (disconnected) throw new BluetoothError('disconnected', 'The recorder disconnected');
    const since = mark();
    await write(`APP&${name}`);
    return since;
  }

  // command sends name and resolves to the value of its answer, or rejects without one.
  async function command(name, answer, timeout = answerTimeout, accept = undefined) {
    const value = await waitFor(answer, await send(name), timeout, accept);
    if (value === null) throw new BluetoothError('no-answer', `No answer to ${name}`);
    return value;
  }

  // values are the values of all answers to answer after since, in order.
  const values = (answer, since) =>
    messages
      .slice(since)
      .map((text) => valueOf(text, answer))
      .filter((value) => value !== null);

  let audio = null;
  async function subscribeAudio() {
    if (audio) return;
    audio = await service.getCharacteristic(AUDIO);
    audio.addEventListener('characteristicvaluechanged', () => undefined);
    await audio.startNotifications();
  }

  return {
    command,
    send,
    waitFor,
    mark,
    values,
    subscribeAudio,
    connected: () => !disconnected,
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

// unlock connects and unlocks the connection with the session key.
async function unlock(sessionKey) {
  const pocket = await connect();
  try {
    if ((await optional(pocket.command(`SK&${sessionKey}`, 'SK'))) !== 'OK') {
      throw new BluetoothError('auth', 'The recorder refused the session key');
    }
  } catch (err) {
    pocket.close();
    throw err;
  }
  return pocket;
}

const failure = (err) => ({
  ok: false,
  error: err instanceof BluetoothError ? err.code : 'failed',
  message: String((err && err.message) || err),
});

// run connects to the recorder, unlocks it with the session key and does action:
//   'check'   reads the battery, firmware, storage and USB state
//   'usb-on'  makes the recorder a USB drive again
// It resolves to {ok: true, ...} or {ok: false, error: code, message}, never rejects.
async function run(action, sessionKey) {
  let pocket = null;
  try {
    pocket = await unlock(sessionKey);
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
    return failure(err);
  } finally {
    pocket?.close();
  }
}

// listRecordings lists the recorder's recordings, oldest first: [{date, timestamp, seconds}].
// Repeats are dropped: some systems deliver each notification several times.
async function listRecordings(pocket) {
  const collect = async (name, answer, end) => {
    const since = await pocket.send(name);
    if ((await pocket.waitFor(end, since, listTimeout)) === null) {
      throw new BluetoothError('no-answer', `No end of the answer to ${name}`);
    }
    return [...new Set(pocket.values(answer, since))];
  };
  const recordings = new Map();
  for (const day of await collect('LIST_DIRS', 'DIRS', 'DIRS_SUM')) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) continue;
    for (const row of await collect(`LIST&${day}`, 'F', 'LIST')) {
      const [date, timestamp, seconds] = row.split('&');
      if (date === day && /^\d{14}$/.test(timestamp || '')) {
        recordings.set(timestamp, { date, timestamp, seconds: parseInt(seconds, 10) || 0 });
      }
    }
  }
  return [...recordings.values()].sort((a, b) => a.timestamp.localeCompare(b.timestamp));
}

// The session's connection, while one is open.
let session = null;

// open starts a session: connects and unlocks. Resolves to {ok} or {ok: false, error, message}.
async function open(sessionKey) {
  try {
    session?.close();
    session = await unlock(sessionKey);
    return { ok: true };
  } catch (err) {
    session = null;
    return failure(err);
  }
}

// accepts are the checks call() can ask waitFor and command for: functions can't be passed.
const accepts = {
  any: () => true,
  digits: (v) => /^\d+$/.test(v.trim()),
  one: (v) => v.trim() === '1',
  pair: (v) => v.includes('&'),
};

// call does one step of the session: method is command, send, waitFor, mark, subscribeAudio,
// listRecordings or connected, args its arguments (accept by name, see accepts). Resolves to
// {ok, value} or {ok: false, error, message}.
async function call(method, args = []) {
  if (!session) return { ok: false, error: 'disconnected', message: 'No open session' };
  try {
    switch (method) {
      case 'command': {
        const [name, answer, timeout, accept] = args;
        return { ok: true, value: await session.command(name, answer, timeout, accepts[accept || 'any']) };
      }
      case 'send':
        return { ok: true, value: await session.send(args[0]) };
      case 'waitFor': {
        const [answer, since, timeout, accept] = args;
        return { ok: true, value: await session.waitFor(answer, since, timeout, accepts[accept || 'any']) };
      }
      case 'mark':
        return { ok: true, value: session.mark() };
      case 'subscribeAudio':
        await session.subscribeAudio();
        return { ok: true };
      case 'listRecordings':
        return { ok: true, value: await listRecordings(session) };
      case 'connected':
        return { ok: true, value: session.connected() };
      default:
        throw new BluetoothError('failed', `Unknown method ${method}`);
    }
  } catch (err) {
    return failure(err);
  }
}

// close ends the session.
function close() {
  session?.close();
  session = null;
  return { ok: true };
}

window.pocketBluetooth = { run, open, call, close };
