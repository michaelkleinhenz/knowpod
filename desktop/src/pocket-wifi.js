// Transfers recordings from a Pocket recorder (heypocketai.com) over its WiFi access point,
// about 1 MB/s instead of Bluetooth's tens of KB/s. Decoded on firmware 1.8 / WiFi firmware V9
// from the Pocket app's own transfer (tools/pocket-wifi-probe/RESEARCH.md has the details):
//
//   1. Over Bluetooth: WIFIO raises the access point, WIFI gives its name and password, and
//      WIFIS goes 3 (starting), 2 (waiting for a client), 1 (a client has joined). The network
//      is hidden, WPA2-PSK; the recorder is 192.168.200.1 and hands out 192.168.200.2.
//   2. Per file: connect to 192.168.200.1:8475 and send nothing; request the file as a
//      Bluetooth transfer (U&<date>&<timestamp> → MCU&U&<size>), and 0.3 s later switch it to
//      WiFi (U&WIFI → MCU&U&WIFI). The socket then carries the MP3 file, exactly <size> bytes,
//      and a fixed 10-byte end marker; MCU&OFF comes over Bluetooth. Close the connection.
//   3. The recorder serves two connections per access point session; then 8475 stops
//      listening until the access point is restarted (WIFIC, WIFIO). U&WIFI must never be
//      sent without a connection open: the recorder hangs until it reports MCU&SHUT.
//
// This machine's WiFi is moved onto the recorder's network and back by a host backend:
// NetworkManager (nmcli) on Linux, netsh on Windows. macOS isn't supported yet (its current
// network can't be read back from the command line since Sonoma).
'use strict';

const { execFile } = require('node:child_process');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const HOST = '192.168.200.1';
const PORT = 8475;
const END_MARKER = Buffer.from('ba5a028f04ba5a028f04', 'hex');
const FILES_PER_SESSION = 2;
// The temporary WiFi profile for the recorder's network.
const PROFILE = 'knowpod-pocket';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// WifiError carries a code for the app to explain: wifi-unsupported, wifi-setup, wifi-join,
// wifi-ap, stuck (the Pocket stopped answering WiFi commands), refused (it wouldn't send a
// recording), transfer or cancelled.
class WifiError extends Error {
  constructor(code, message) {
    super(message || code);
    this.code = code;
  }
}

// run runs a program and resolves to {ok, out, err}; it never rejects.
function run(file, args, timeout = 30_000) {
  return new Promise((resolve) => {
    execFile(file, args, { timeout, windowsHide: true, maxBuffer: 4 * 1024 * 1024 }, (err, stdout, stderr) => {
      resolve({ ok: !err, out: String(stdout || ''), err: String(stderr || (err && err.message) || '') });
    });
  });
}

const lastLine = (r) => (r.err || r.out).trim().split('\n').pop() || 'failed';

const sameSubnet = (a, b) => !!a && a.split('.').slice(0, 3).join('.') === b.split('.').slice(0, 3).join('.');

// addressOnAp is this machine's address on the recorder's network, or null.
function addressOnAp(host = HOST) {
  for (const addresses of Object.values(os.networkInterfaces())) {
    for (const a of addresses || []) if (a.family === 'IPv4' && sameSubnet(a.address, host)) return a.address;
  }
  return null;
}

async function waitForAddress(host, ms) {
  const end = Date.now() + ms;
  for (;;) {
    const address = addressOnAp(host);
    if (address || Date.now() >= end) return address;
    await sleep(500);
  }
}

// splitTerse splits a line of `nmcli -t` output, which escapes ':' inside fields as '\:'.
function splitTerse(line) {
  const fields = [];
  let current = '';
  let escaped = false;
  for (const ch of line) {
    if (escaped) {
      current += ch;
      escaped = false;
    } else if (ch === '\\') escaped = true;
    else if (ch === ':') {
      fields.push(current);
      current = '';
    } else current += ch;
  }
  fields.push(current);
  return fields;
}

// networkManager moves the WiFi of a Linux machine with NetworkManager. The profile is a
// hidden network that never becomes the default route, so a wired connection keeps internet.
function networkManager(host, log) {
  let iface = null;
  let original = null;
  const state = async () => {
    const r = await run('nmcli', ['-t', '-f', 'GENERAL.STATE', 'connection', 'show', PROFILE], 5_000);
    return r.ok ? r.out.trim().split(':').pop() : '';
  };
  return {
    async setup() {
      const r = await run('nmcli', ['-t', '-f', 'DEVICE,TYPE,STATE', 'device']);
      if (!r.ok) throw new WifiError('wifi-setup', `NetworkManager isn't usable: ${lastLine(r)}`);
      const wifi = r.out.split('\n').filter(Boolean).map(splitTerse).find((d) => d[1] === 'wifi');
      if (!wifi) throw new WifiError('wifi-setup', 'NetworkManager has no WiFi device');
      iface = wifi[0];
      const active = await run('nmcli', ['-t', '-f', 'NAME,UUID,TYPE,DEVICE', 'connection', 'show', '--active']);
      const current = active.out.split('\n').filter(Boolean).map(splitTerse).find((d) => d[3] === iface);
      original = current ? current[1] : null;
      log(`WiFi: ${iface}, currently ${current ? `on "${current[0]}"` : 'not connected'}`);
    },
    async prepare(ssid, password) {
      await run('nmcli', ['connection', 'delete', PROFILE]);
      const r = await run('nmcli', [
        'connection', 'add', 'type', 'wifi', 'ifname', iface, 'con-name', PROFILE, 'ssid', ssid,
        'autoconnect', 'no', '802-11-wireless.hidden', 'yes',
        'wifi-sec.key-mgmt', 'wpa-psk', 'wifi-sec.psk', password,
        'ipv4.method', 'auto', 'ipv4.never-default', 'yes', 'ipv6.method', 'disabled',
      ]);
      if (!r.ok) throw new WifiError('wifi-setup', `Couldn't create the WiFi profile: ${lastLine(r)}`);
    },
    // join never asks again while a connection is activating: that disconnects and reconnects,
    // and the recorder drops its transfer socket when its client leaves.
    async join(ssid, deadline) {
      let attempt = 0;
      while (Date.now() < deadline) {
        if (['activating', 'activated'].includes(await state())) {
          if (await waitForAddress(host, 1_000)) return true;
          continue;
        }
        attempt++;
        const wait = Math.max(3, Math.round((deadline - Date.now()) / 1000));
        const r = await run('nmcli', ['--wait', String(wait), 'connection', 'up', PROFILE, 'ifname', iface], (wait + 5) * 1000);
        if (await waitForAddress(host, r.ok ? 5_000 : 500)) return true;
        log(`WiFi join attempt ${attempt}: ${lastLine(r)}`);
        if (!['activating', 'activated'].includes(await state())) {
          await run('nmcli', ['device', 'wifi', 'rescan', 'ifname', iface, 'ssid', ssid], 5_000);
          await sleep(1_000);
        }
      }
      return false;
    },
    async leave() {
      await run('nmcli', ['connection', 'down', PROFILE], 15_000);
    },
    async restore() {
      if (original) {
        const r = await run('nmcli', ['--wait', '30', 'connection', 'up', 'uuid', original], 40_000);
        if (!r.ok) log(`WiFi: couldn't rejoin the usual network: ${lastLine(r)}`);
      } else {
        await run('nmcli', ['connection', 'down', PROFILE]);
      }
      await run('nmcli', ['connection', 'delete', PROFILE]);
    },
  };
}

// netsh labels are translated; these cover English and German.
const netshKeys = { name: 'name', profile: 'profile', profil: 'profile', ssid: 'ssid', state: 'state', status: 'state' };

// parseNetshInterfaces reads `netsh wlan show interfaces`: [{name, profile, ssid, state}].
function parseNetshInterfaces(text) {
  const found = [];
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*([^:]+?)\s*:\s(.*)$/.exec(line);
    const key = m && netshKeys[m[1].trim().toLowerCase()];
    if (!key) continue;
    if (key === 'name') found.push({});
    if (found.length && found[found.length - 1][key] === undefined) found[found.length - 1][key] = m[2].trim();
  }
  return found;
}

const xmlEscape = (text) =>
  String(text).replace(/[<>&'"]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', "'": '&apos;', '"': '&quot;' })[c]);

// windowsProfile is a WLAN profile for the recorder's hidden network.
function windowsProfile(ssid, password) {
  return `<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
  <name>${PROFILE}</name>
  <SSIDConfig>
    <SSID><name>${xmlEscape(ssid)}</name></SSID>
    <nonBroadcast>true</nonBroadcast>
  </SSIDConfig>
  <connectionType>ESS</connectionType>
  <connectionMode>manual</connectionMode>
  <MSM>
    <security>
      <authEncryption>
        <authentication>WPA2PSK</authentication>
        <encryption>AES</encryption>
        <useOneX>false</useOneX>
      </authEncryption>
      <sharedKey>
        <keyType>passPhrase</keyType>
        <protected>false</protected>
        <keyMaterial>${xmlEscape(password)}</keyMaterial>
      </sharedKey>
    </security>
  </MSM>
</WLANProfile>
`;
}

// netsh moves the WiFi of a Windows machine.
function netsh(host, log) {
  let iface = null;
  let original = null;
  const disconnected = async () => {
    const r = await run('netsh', ['wlan', 'show', 'interfaces'], 5_000);
    const me = parseNetshInterfaces(r.out).find((i) => i.name === iface) || {};
    return ['disconnected', 'getrennt'].includes(String(me.state || '').toLowerCase());
  };
  return {
    async setup() {
      const r = await run('netsh', ['wlan', 'show', 'interfaces']);
      const found = parseNetshInterfaces(r.out);
      if (!r.ok || !found.length) throw new WifiError('wifi-setup', 'Windows has no WiFi interface');
      iface = found[0].name;
      const state = String(found[0].state || '').toLowerCase();
      original = ['connected', 'verbunden'].includes(state) ? found[0].profile || null : null;
      log(`WiFi: ${iface}, currently ${original ? `on "${original}"` : 'not connected'}`);
    },
    async prepare(ssid, password) {
      await run('netsh', ['wlan', 'delete', 'profile', `name=${PROFILE}`, `interface=${iface}`]);
      const file = path.join(os.tmpdir(), `${PROFILE}-${process.pid}.xml`);
      fs.writeFileSync(file, windowsProfile(ssid, password), { mode: 0o600 });
      try {
        const r = await run('netsh', ['wlan', 'add', 'profile', `filename=${file}`, `interface=${iface}`, 'user=current']);
        if (!r.ok) throw new WifiError('wifi-setup', `Couldn't create the WiFi profile: ${lastLine(r)}`);
      } finally {
        fs.rmSync(file, { force: true });
      }
    },
    // join asks again only once the interface is disconnected: a second connect while Windows
    // is still associating restarts it.
    async join(ssid, deadline) {
      let attempt = 0;
      while (Date.now() < deadline) {
        attempt++;
        const r = await run('netsh', ['wlan', 'connect', `name=${PROFILE}`, `ssid=${ssid}`, `interface=${iface}`]);
        if (!r.ok) {
          log(`WiFi join attempt ${attempt}: ${lastLine(r)}`);
          await sleep(1_000);
          continue;
        }
        while (Date.now() < deadline) {
          if (await waitForAddress(host, 1_000)) return true;
          if (await disconnected()) break;
        }
      }
      return false;
    },
    async leave() {
      await run('netsh', ['wlan', 'disconnect', `interface=${iface}`]);
    },
    async restore() {
      if (original) {
        const r = await run('netsh', ['wlan', 'connect', `name=${original}`, `interface=${iface}`]);
        if (!r.ok) log(`WiFi: couldn't rejoin the usual network: ${lastLine(r)}`);
      } else {
        await run('netsh', ['wlan', 'disconnect', `interface=${iface}`]);
      }
      await run('netsh', ['wlan', 'delete', 'profile', `name=${PROFILE}`, `interface=${iface}`]);
    },
  };
}

// wifiSupported says whether this machine's WiFi can be moved onto the recorder's network.
const wifiSupported = () => process.platform === 'linux' || process.platform === 'win32';

function hostWifi(host, log) {
  if (process.platform === 'linux') return networkManager(host, log);
  if (process.platform === 'win32') return netsh(host, log);
  throw new WifiError('wifi-unsupported', `WiFi transfer isn't supported on ${process.platform} yet`);
}

// openSocket connects to the transfer socket, retrying refusals for wait ms: after a
// connection closes, the recorder refuses new ones for a few seconds. The socket keeps an
// error handler for its whole life: the recorder may reset a connection at any time (also
// one it has just accepted), and an 'error' event without a handler would take the app down.
// What happened is kept in socket.transferError; see alive().
async function openSocket(host, port, wait) {
  const deadline = Date.now() + wait;
  let last = null;
  for (;;) {
    try {
      return await new Promise((resolve, reject) => {
        const socket = net.connect({ host, port });
        const timer = setTimeout(() => {
          socket.destroy();
          reject(new Error('connect timed out'));
        }, 3_000);
        const onConnectError = (err) => {
          clearTimeout(timer);
          reject(err);
        };
        socket.once('connect', () => {
          clearTimeout(timer);
          socket.removeListener('error', onConnectError);
          socket.on('error', (err) => {
            socket.transferError = err;
          });
          socket.on('close', () => {
            socket.transferClosed = true;
          });
          resolve(socket);
        });
        socket.once('error', onConnectError);
      });
    } catch (err) {
      last = err;
    }
    if (Date.now() >= deadline) throw new WifiError('transfer', `The Pocket didn't accept a connection (${last?.message || last})`);
    await sleep(500);
  }
}

// alive says whether a transfer connection is still open: not reset, closed or ended by the
// recorder.
const alive = (socket) => !socket.destroyed && !socket.transferError && !socket.transferClosed && socket.readyState === 'open';

// closeSocket ends a connection gracefully (FIN, as verified on the recorder) and resolves once
// it's closed, destroying it after ms at the latest.
function closeSocket(socket, ms = 2_000) {
  if (socket.destroyed) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(() => {
      socket.destroy();
      resolve();
    }, ms);
    socket.once('close', () => {
      clearTimeout(timer);
      resolve();
    });
    socket.end();
  });
}

// receiveFile reads exactly size bytes of file from socket into file, then the end marker.
// It writes to <file>.part and renames it only once all of it arrived. Resolves to whether
// the end marker followed (a missing one doesn't fail an otherwise complete file).
function receiveFile(socket, size, file, { firstByteTimeout = 15_000, idleTimeout = 15_000, markerTimeout = 3_000, onProgress } = {}) {
  const partial = `${file}.part`;
  return new Promise((resolve, reject) => {
    let fd;
    try {
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fd = fs.openSync(partial, 'w');
    } catch (err) {
      reject(err);
      return;
    }
    let received = 0;
    let tail = Buffer.alloc(0);
    let timer = null;
    let done = false;
    const finish = (err) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      socket.removeListener('data', onData);
      socket.removeListener('close', onClose);
      socket.removeListener('error', onError);
      try {
        fs.closeSync(fd);
      } catch {
        // already closed
      }
      if (err) {
        fs.rmSync(partial, { force: true });
        reject(err);
        return;
      }
      try {
        fs.renameSync(partial, file);
        resolve(tail.subarray(0, END_MARKER.length).equals(END_MARKER));
      } catch (renameErr) {
        fs.rmSync(partial, { force: true });
        reject(renameErr);
      }
    };
    const arm = (ms, what) => {
      clearTimeout(timer);
      timer = setTimeout(() => (received >= size ? finish(null) : finish(new WifiError('transfer', what()))), ms);
    };
    function onData(chunk) {
      if (received < size) {
        const body = chunk.subarray(0, size - received);
        try {
          fs.writeSync(fd, body);
        } catch (err) {
          finish(err);
          return;
        }
        received += body.length;
        tail = Buffer.concat([tail, chunk.subarray(body.length)]);
        onProgress?.(received, size);
      } else {
        tail = Buffer.concat([tail, chunk]);
      }
      if (received >= size) {
        if (tail.length >= END_MARKER.length) finish(null);
        else arm(markerTimeout, () => '');
      } else {
        arm(idleTimeout, () => `The transfer stalled at ${received} of ${size} bytes`);
      }
    }
    function onClose() {
      if (received >= size) finish(null);
      else finish(new WifiError('transfer', `The Pocket closed the connection at ${received} of ${size} bytes`));
    }
    function onError(err) {
      finish(received >= size ? null : new WifiError('transfer', err.message));
    }
    socket.on('data', onData);
    socket.on('close', onClose);
    socket.on('error', onError);
    if (size === 0) arm(markerTimeout, () => '');
    else arm(firstByteTimeout, () => 'The Pocket sent nothing');
  });
}

// createWifiSession drives the recorder's access point over the Bluetooth session ble (see
// pocket-bluetooth.js: session()) and moves this machine's WiFi onto it. options:
//   log(text), onRestart() (the access point is being restarted for more files),
//   wifi (the host backend; by default the one for this OS), host, port, timings (for tests)
function createWifiSession(
  ble,
  { host = HOST, port = PORT, log = () => undefined, onRestart = () => undefined, wifi: hostBackend = null, timings = {} } = {},
) {
  // settle: how long a new connection must stay open before a file is requested on it (the
  // recorder sometimes accepts a connection and resets it right away); reconnectPause: the
  // pause between closing one connection and opening the next (the recorder refuses for a
  // few seconds after a close; this is the timing verified on the recorder).
  const t = {
    join: 90_000,
    restartPause: 2_000,
    switchDelay: 300,
    heartbeat: 5_000,
    poll: 1_000,
    settle: 500,
    reconnectPause: 2_000,
    offWait: 10_000,
    ...timings,
  };
  let wifi = hostBackend;
  let ssid = null;
  let raised = false;
  let prepared = false;
  let connections = 0;
  let heartbeat = null;
  let socket = null;
  let cancelled = false;
  let closedAt = 0; // when the last transfer connection was closed

  const check = () => {
    if (cancelled) throw new WifiError('cancelled');
  };

  // raise starts the access point and joins it: WIFIO, WIFI for the credentials, then join
  // while WIFIS is polled until it reports a client (1) — the Pocket app's order.
  async function raise() {
    const since = await ble.mark();
    raised = true;
    const started = Date.now();
    try {
      await ble.command('WIFIO', 'WIFIO');
    } catch (err) {
      // No answer: the recorder hangs (e.g. after a switch it couldn't serve). Every later
      // attempt would fail the same way, so the copy stops here.
      if (err?.code === 'no-answer') throw new WifiError('stuck', 'The Pocket stopped answering WiFi commands');
      throw err;
    }
    if (!ssid) {
      const credentials = await ble.command('WIFI', 'WIFI', 5_000, 'pair');
      const [name, password] = [credentials.slice(0, credentials.indexOf('&')), credentials.slice(credentials.indexOf('&') + 1)];
      ssid = name;
      await wifi.prepare(name, password);
      prepared = true;
    }
    check();
    const poll = setInterval(() => void ble.send('WIFIS').catch(() => undefined), t.poll);
    try {
      if (!(await wifi.join(ssid, started + t.join))) throw new WifiError('wifi-join', `Couldn't join ${ssid}`);
      check();
      const left = Math.max(1_000, started + t.join - Date.now());
      if ((await ble.waitFor('WIFIS', since, left, 'one')) === null) {
        throw new WifiError('wifi-ap', 'The Pocket never reported this computer on its WiFi');
      }
    } finally {
      clearInterval(poll);
    }
    connections = 0;
    log(`On ${ssid}`);
  }

  return {
    async start() {
      wifi = wifi || hostWifi(host, log);
      await wifi.setup();
      await ble.subscribeAudio();
      // The Pocket app keeps a heartbeat going while the access point is up.
      heartbeat = setInterval(() => void ble.send('WPING').catch(() => undefined), t.heartbeat);
      await raise();
    },

    // download transfers one recording ({date, timestamp}) into file and resolves to
    // {size, markerOk}. onProgress(received, size).
    async download(recording, file, onProgress) {
      check();
      if (connections >= FILES_PER_SESSION) {
        onRestart();
        await ble.command('WIFIC', 'WIFIC').catch(() => undefined);
        raised = false;
        await wifi.leave();
        await sleep(t.restartPause);
        check();
        await raise();
      }
      const pause = closedAt + t.reconnectPause - Date.now();
      if (pause > 0) await sleep(pause);
      // The connection must be open BEFORE the switch (U&WIFI).
      try {
        socket = await openSocket(host, port, 15_000);
      } catch (err) {
        connections = FILES_PER_SESSION; // not listening: a fresh access point may help
        throw err;
      }
      connections++;
      const current = socket;
      try {
        check();
        // An accepted connection the recorder resets shows up within moments; asking for the
        // file on it would start a transfer nobody can receive.
        await sleep(t.settle);
        if (!alive(current)) {
          throw new WifiError('transfer', `The Pocket dropped the connection (${current.transferError?.message || 'closed'})`);
        }
        const answer = await ble.command(`U&${recording.date}&${recording.timestamp}`, 'U', 10_000);
        if (!/^\d+$/.test(answer.trim())) {
          throw new WifiError('refused', `The Pocket wouldn't send ${recording.timestamp} (MCU&U&${answer})`);
        }
        const size = parseInt(answer, 10);
        await sleep(t.switchDelay);
        // Never switch without an open connection: the recorder would hang (MCU&SHUT).
        if (!alive(current)) {
          throw new WifiError('transfer', `The connection was lost before the switch (${current.transferError?.message || 'closed'})`);
        }
        const since = await ble.send('U&WIFI');
        const markerOk = await receiveFile(current, size, file, { onProgress });
        if ((await ble.waitFor('OFF', since, t.offWait)) === null) log('The Pocket did not report the end of the transfer');
        return { size, markerOk };
      } catch (err) {
        // The recorder's state is unknown now: the next file starts on a fresh access point.
        connections = FILES_PER_SESSION;
        if (cancelled) throw new WifiError('cancelled');
        throw err;
      } finally {
        await closeSocket(current);
        closedAt = Date.now();
        if (socket === current) socket = null;
      }
    },

    // cancel stops a transfer that runs now; download() then rejects with 'cancelled'.
    cancel() {
      cancelled = true;
      socket?.destroy();
    },

    // close lowers the access point and puts this machine's WiFi back. Never rejects.
    async close() {
      clearInterval(heartbeat);
      if (raised) {
        raised = false;
        await ble.command('WIFIC', 'WIFIC').catch(() => undefined);
      }
      if (prepared) {
        prepared = false;
        try {
          await wifi.restore();
        } catch (err) {
          log(`WiFi: couldn't restore: ${err.message}`);
        }
      }
    },
  };
}

module.exports = {
  createWifiSession,
  wifiSupported,
  WifiError,
  // for tests
  receiveFile,
  openSocket,
  parseNetshInterfaces,
  splitTerse,
  windowsProfile,
  END_MARKER,
};
