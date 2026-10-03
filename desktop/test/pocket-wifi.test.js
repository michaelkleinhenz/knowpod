// Tests for the WiFi transfer (src/pocket-wifi.js) against a fake recorder that behaves like
// the real one measured on firmware 1.8 (tools/pocket-wifi-probe/RESEARCH.md): it serves the
// transfer socket for two connections per access point session, sends a file and the end
// marker only on a connection that is open when U&WIFI comes, and answers like the recorder.
// Run with: npm test (plain node:test, no Electron needed).
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { test } = require('node:test');

const {
  createWifiSession,
  receiveFile,
  parseNetshInterfaces,
  splitTerse,
  windowsProfile,
  END_MARKER,
} = require('../src/pocket-wifi');

const mp3 = (n, seed = 0) => {
  const out = Buffer.alloc(n);
  for (let i = 0; i < n; i++) out[i] = (i * 7 + seed) & 0xff;
  out.write('fff348c4', 0, 'hex');
  return out;
};

const FILES = {
  20261003142550: mp3(885_788, 1),
  20261003141332: mp3(722_348, 2),
  20261003160116: mp3(92_062, 3),
};

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-wifi-test-'));

// fakeRecorder is the recorder behind a fake Bluetooth session (the shape of
// pocket-bluetooth.js: session()). Failures seen on the real one can be switched on:
//   resetAfterFile  reset the connection right after sending a file (ECONNRESET)
//   resetAccepted   numbers of accepted connections (counting all) to reset right away
//   answers         {timestamp: value} answers to U&<date>&<timestamp> other than the size
//   wifioAnswers    how many APP&WIFIO it answers before it hangs
function fakeRecorder({ marker = true, files = FILES, resetAfterFile = false, resetAccepted = [], answers = {}, wifioAnswers = Infinity } = {}) {
  let acceptedTotal = 0;
  const messages = [];
  const listeners = new Set();
  const sent = [];
  const violations = [];
  let server = null;
  let accepted = 0;
  let current = null; // the open transfer connection
  let staged = null;
  const state = { status: 0, apStarts: 0, port: 0 };
  const say = (text) => {
    messages.push(text);
    listeners.forEach((l) => l());
  };

  async function startAp() {
    accepted = 0;
    state.apStarts++;
    server = net.createServer((socket) => {
      accepted++;
      acceptedTotal++;
      if (resetAccepted.includes(acceptedTotal)) {
        // Shortly after the handshake, as seen on the real recorder: the client is connected.
        socket.on('error', () => undefined);
        setTimeout(() => socket.resetAndDestroy(), 10);
        if (accepted >= 2) server.close();
        return;
      }
      current = socket;
      socket.on('close', () => {
        if (current === socket) current = null;
      });
      socket.on('error', () => undefined);
      if (accepted >= 2) server.close(); // stops listening: connects are refused
    });
    await new Promise((resolve) => server.listen(state.port, '127.0.0.1', resolve));
    state.port = server.address().port;
  }

  async function handle(name) {
    sent.push(name);
    if (name === 'WIFIO' && wifioAnswers-- <= 0) {
      return; // hangs: no answer
    }
    if (name === 'WIFIO') {
      state.status = 3;
      await startAp();
      say('MCU&WIFIO');
      state.status = 2;
    } else if (name === 'WIFI') say('MCU&WIFI&PKT01_GREY_TEST&abcd1234');
    else if (name === 'WIFIS') say(`MCU&WIFIS&${state.status}`);
    else if (name === 'WPING') say('MCU&WPING');
    else if (name === 'WIFIC') {
      server?.close();
      state.status = 0;
      say('MCU&WIFIC');
    } else if (name === 'U&WIFI') {
      if (!current) {
        violations.push('U&WIFI without an open connection');
        return;
      }
      say('MCU&U&WIFI');
      say(`MCU&U&${staged.length}`);
      const socket = current;
      socket.write(marker ? Buffer.concat([staged, END_MARKER]) : staged, () => {
        say('MCU&OFF');
        if (resetAfterFile) setTimeout(() => socket.resetAndDestroy(), 5);
      });
    } else if (name.startsWith('U&')) {
      const timestamp = name.split('&')[2];
      if (answers[timestamp] !== undefined) {
        say(`MCU&U&${answers[timestamp]}`);
        return;
      }
      staged = files[timestamp];
      say(`MCU&U&${staged.length}`);
    }
  }

  const valueOf = (text, answer) => {
    const exact = `MCU&${answer}`;
    if (text === exact) return '';
    return text.startsWith(`${exact}&`) ? text.slice(exact.length + 1) : null;
  };
  const accepts = { any: () => true, digits: (v) => /^\d+$/.test(v), one: (v) => v === '1', pair: (v) => v.includes('&') };

  const ble = {
    mark: async () => messages.length,
    send: async (name) => {
      const since = messages.length;
      await handle(name);
      return since;
    },
    waitFor: (answer, since, timeout, accept = 'any') =>
      new Promise((resolve) => {
        let i = since;
        const look = () => {
          while (i < messages.length) {
            const v = valueOf(messages[i++], answer);
            if (v !== null && accepts[accept](v)) {
              listeners.delete(look);
              clearTimeout(timer);
              resolve(v);
              return;
            }
          }
        };
        const timer = setTimeout(() => {
          listeners.delete(look);
          resolve(null);
        }, timeout);
        listeners.add(look);
        look();
      }),
    command: async (name, answer, timeout = 2_000, accept = 'any') => {
      const v = await ble.waitFor(answer, await ble.send(name), timeout, accept);
      if (v === null) throw Object.assign(new Error(`No answer to ${name}`), { code: 'no-answer' });
      return v;
    },
    subscribeAudio: async () => undefined,
  };

  const calls = [];
  const wifi = {
    setup: async () => calls.push('setup'),
    prepare: async (ssid, password) => calls.push(`prepare ${ssid} ${password}`),
    join: async () => {
      calls.push('join');
      state.status = 1;
      return true;
    },
    leave: async () => calls.push('leave'),
    restore: async () => calls.push('restore'),
  };
  return { ble, wifi, calls, sent, violations, state, stop: () => server?.close() };
}

const timings = { restartPause: 10, switchDelay: 5, heartbeat: 20, poll: 10, join: 5_000, settle: 30, reconnectPause: 20, offWait: 500 };

// freePort is a port nothing listens on, for the fake access point.
async function freePort() {
  const probe = net.createServer();
  await new Promise((resolve) => probe.listen(0, '127.0.0.1', resolve));
  const { port } = probe.address();
  await new Promise((resolve) => probe.close(resolve));
  return port;
}

async function startSession(recorder) {
  recorder.state.port = await freePort();
  const wifiSession = createWifiSession(recorder.ble, { host: '127.0.0.1', port: recorder.state.port, wifi: recorder.wifi, timings });
  await wifiSession.start();
  return wifiSession;
}

test('three files: the access point is restarted after two, files arrive without the marker', async () => {
  const recorder = fakeRecorder();
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  const names = Object.keys(FILES);
  const results = [];
  for (const ts of names) {
    results.push(await wifiSession.download({ date: '2026-10-03', timestamp: ts }, path.join(dir, `${ts}.mp3`)));
  }
  await wifiSession.close();
  recorder.stop();

  for (const [i, ts] of names.entries()) {
    assert.ok(fs.readFileSync(path.join(dir, `${ts}.mp3`)).equals(FILES[ts]), `${ts} arrived intact`);
    assert.equal(results[i].size, FILES[ts].length);
    assert.equal(results[i].markerOk, true);
  }
  assert.equal(recorder.state.apStarts, 2);
  assert.deepEqual(recorder.violations, []);
  assert.deepEqual(recorder.calls, ['setup', 'prepare PKT01_GREY_TEST abcd1234', 'join', 'leave', 'join', 'restore']);
  assert.equal(recorder.sent.at(-1), 'WIFIC');
  assert.deepEqual(fs.readdirSync(dir).filter((n) => n.endsWith('.part')), []);
});

test("the app's order: WIFIO, WIFI, the file request, then the switch", async () => {
  const recorder = fakeRecorder();
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  await wifiSession.download({ date: '2026-10-03', timestamp: '20261003160116' }, path.join(dir, 'a.mp3'));
  await wifiSession.close();
  recorder.stop();
  const commands = recorder.sent.filter((c) => c !== 'WIFIS' && c !== 'WPING');
  assert.deepEqual(commands, ['WIFIO', 'WIFI', 'U&2026-10-03&20261003160116', 'U&WIFI', 'WIFIC']);
});

// ── Failures seen on the real recorder ──

test('a reset after the file neither crashes the app nor loses the file', async () => {
  // An 'error' event without a handler would be an uncaught exception here.
  const recorder = fakeRecorder({ resetAfterFile: true });
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  const names = Object.keys(FILES);
  for (const ts of names) await wifiSession.download({ date: '2026-10-03', timestamp: ts }, path.join(dir, `${ts}.mp3`));
  await wifiSession.close();
  recorder.stop();
  for (const ts of names) assert.ok(fs.readFileSync(path.join(dir, `${ts}.mp3`)).equals(FILES[ts]));
});

test('a connection reset right after it was accepted is never switched to', async () => {
  const recorder = fakeRecorder({ resetAccepted: [2] });
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  const [first, second, third] = Object.keys(FILES);
  await wifiSession.download({ date: '2026-10-03', timestamp: first }, path.join(dir, 'a.mp3'));
  await assert.rejects(wifiSession.download({ date: '2026-10-03', timestamp: second }, path.join(dir, 'b.mp3')), { code: 'transfer' });
  // The next file goes over a restarted access point.
  await wifiSession.download({ date: '2026-10-03', timestamp: third }, path.join(dir, 'c.mp3'));
  await wifiSession.close();
  recorder.stop();
  assert.deepEqual(recorder.violations, []);
  assert.ok(!recorder.sent.includes(`U&2026-10-03&${second}`), 'no file requested on the dead connection');
  assert.equal(recorder.state.apStarts, 2);
  assert.ok(fs.readFileSync(path.join(dir, 'c.mp3')).equals(FILES[third]));
});

test('a recording the recorder refuses fails alone, with its answer', async () => {
  const [first, second] = Object.keys(FILES);
  const recorder = fakeRecorder({ answers: { [first]: 'ERR' } });
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  await assert.rejects(wifiSession.download({ date: '2026-10-03', timestamp: first }, path.join(dir, 'a.mp3')), {
    code: 'refused',
    message: /MCU&U&ERR/,
  });
  await wifiSession.download({ date: '2026-10-03', timestamp: second }, path.join(dir, 'b.mp3'));
  await wifiSession.close();
  recorder.stop();
  assert.ok(!recorder.sent.slice(0, recorder.sent.indexOf(`U&2026-10-03&${second}`)).includes('U&WIFI'), 'no switch for the refused one');
  assert.ok(fs.readFileSync(path.join(dir, 'b.mp3')).equals(FILES[second]));
});

test('a recorder that stops answering WIFIO stops the copy as stuck', async () => {
  const recorder = fakeRecorder({ answers: { 20261003142550: 'ERR' }, wifioAnswers: 1 });
  const dir = tmp();
  const wifiSession = await startSession(recorder);
  await assert.rejects(wifiSession.download({ date: '2026-10-03', timestamp: '20261003142550' }, path.join(dir, 'a.mp3')), { code: 'refused' });
  // The failure forces a restart of the access point, which the recorder doesn't answer.
  await assert.rejects(wifiSession.download({ date: '2026-10-03', timestamp: '20261003141332' }, path.join(dir, 'b.mp3')), { code: 'stuck' });
  await wifiSession.close();
  recorder.stop();
});

// ── receiveFile ──

function pair() {
  return new Promise((resolve) => {
    const server = net.createServer((remote) => resolve({ server, remote, local }));
    let local;
    server.listen(0, '127.0.0.1', () => {
      local = net.connect(server.address().port, '127.0.0.1');
    });
  });
}

test('receiveFile splits the marker off', async () => {
  const { server, remote, local } = await pair();
  const body = mp3(5000);
  const file = path.join(tmp(), 'f.mp3');
  const done = receiveFile(local, body.length, file);
  remote.write(Buffer.concat([body, END_MARKER]));
  assert.equal(await done, true);
  assert.ok(fs.readFileSync(file).equals(body));
  local.destroy();
  remote.destroy();
  server.close();
});

test('receiveFile rejects a short transfer and leaves nothing', async () => {
  const { server, remote, local } = await pair();
  const dir = tmp();
  const done = receiveFile(local, 5000, path.join(dir, 'f.mp3'));
  remote.end(mp3(100));
  await assert.rejects(done, { code: 'transfer' });
  assert.deepEqual(fs.readdirSync(dir), []);
  local.destroy();
  server.close();
});

test('receiveFile times out when nothing arrives', async () => {
  const { server, remote, local } = await pair();
  const dir = tmp();
  await assert.rejects(receiveFile(local, 10, path.join(dir, 'f.mp3'), { firstByteTimeout: 100 }), { code: 'transfer' });
  assert.deepEqual(fs.readdirSync(dir), []);
  local.destroy();
  remote.destroy();
  server.close();
});

test('a missing marker keeps the complete file', async () => {
  const { server, remote, local } = await pair();
  const body = mp3(2000);
  const file = path.join(tmp(), 'f.mp3');
  const done = receiveFile(local, body.length, file, { markerTimeout: 100 });
  remote.write(body);
  assert.equal(await done, false);
  assert.ok(fs.readFileSync(file).equals(body));
  local.destroy();
  remote.destroy();
  server.close();
});

// ── host helpers ──

test('splitTerse unescapes colons', () => {
  assert.deepEqual(splitTerse('Home\\:Net:1234:802-11-wireless:wlan0'), ['Home:Net', '1234', '802-11-wireless', 'wlan0']);
});

test('netsh output in English and German', () => {
  const en = '    Name                   : Wi-Fi\r\n    State                  : connected\r\n    SSID                   : Home\r\n    Profile                : Home\r\n';
  const de = '    Name                   : WLAN\n    Status                 : Verbunden\n    SSID                   : Zuhause\n    Profil                 : Zuhause\n';
  assert.deepEqual(parseNetshInterfaces(en), [{ name: 'Wi-Fi', state: 'connected', ssid: 'Home', profile: 'Home' }]);
  assert.equal(parseNetshInterfaces(de)[0].profile, 'Zuhause');
});

test('the Windows profile is a hidden network, escaped', () => {
  const xml = windowsProfile('A&B', 'p<w>');
  assert.match(xml, /<name>A&amp;B<\/name>/);
  assert.match(xml, /p&lt;w&gt;/);
  assert.match(xml, /<nonBroadcast>true<\/nonBroadcast>/);
});
