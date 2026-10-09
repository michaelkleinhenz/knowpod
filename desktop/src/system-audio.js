// Records what the computer plays, for recording a video meeting from the same machine in the
// meeting recorder (frontend/src/components/MeetingRecorder.tsx).
//
// Windows and macOS (13 and later): Chromium records it itself as the "loopback" audio of a
// screen capture. list() offers it as the one output 'loopback', start() lets the page's next
// getDisplayMedia call have it for a few seconds (main.js grants it in its display media
// handler, see takeLoopback) and the page records the stream it gets (src/lib/systemAudio.ts).
// Windows records the default output, macOS everything the computer plays; macOS asks for the
// permission to record the system audio (NSAudioCaptureUsageDescription in package.json).
//
// Linux: Chromium
// lists microphones but not the "monitor" sources that PulseAudio and PipeWire keep of every
// output, so this captures them itself: list() names the outputs (pactl list sinks), start()
// records one output's monitor with parec as 32-bit float mono PCM at the page's sample rate
// and hands each chunk to onData. The page plays the chunks into its own audio graph
// (recorder-worklet.js) beside the microphone.
//
// Needs pactl and parec (pulseaudio-utils), which also work with PipeWire's PulseAudio server.
'use strict';

const { execFile, spawn } = require('node:child_process');
const os = require('node:os');

// loopback says whether Chromium records what the computer plays (Windows, macOS 13 and later,
// Darwin 22), rather than parec.
const loopback = (platform = process.platform, release = os.release()) =>
  platform === 'win32' || (platform === 'darwin' && parseInt(release, 10) >= 22);

// supported says whether this computer can record its outputs.
const supported = (platform = process.platform, release = os.release()) => platform === 'linux' || loopback(platform, release);

// LOOPBACK is the one output offered where Chromium records: what the computer plays.
const LOOPBACK = 'loopback';
// A granted loopback capture must be asked for within this time.
const loopbackGrace = 10_000;

// The tools print English when told to, so their output can be parsed.
const env = () => ({ ...process.env, LC_ALL: 'C', LANG: 'C' });

function run(file, args) {
  return new Promise((resolve, reject) => {
    execFile(file, args, { env: env(), timeout: 5_000, maxBuffer: 4 * 1024 * 1024 }, (err, stdout) => {
      if (err) reject(err);
      else resolve(String(stdout));
    });
  });
}

// parseSinks reads the outputs out of `pactl list sinks`: [{name, description}].
function parseSinks(text) {
  const sinks = [];
  let current = null;
  for (const line of text.split('\n')) {
    if (/^Sink #\d+/.test(line)) {
      current = { name: '', description: '' };
      sinks.push(current);
      continue;
    }
    if (!current) continue;
    const m = /^\s+(Name|Description):\s*(.*)$/.exec(line);
    if (m && m[1] === 'Name' && !current.name) current.name = m[2].trim();
    if (m && m[1] === 'Description' && !current.description) current.description = m[2].trim();
  }
  return sinks.filter((s) => s.name).map((s) => ({ name: s.name, description: s.description || s.name }));
}

// errorCode tells the page why recording the outputs can't work: missing-tools (no pactl or
// parec), no-server (no PulseAudio or PipeWire running) or failed.
function errorCode(err) {
  if (err && err.code === 'ENOENT') return 'missing-tools';
  if (/connection refused|connection failure|no such file or directory|access denied/i.test(String(err && (err.stderr || err.message)))) return 'no-server';
  return 'failed';
}

// list returns the outputs, the default one marked: {ok, sinks: [{name, description,
// default}], loopback}, or {ok: false, error, message}. With loopback the one output is
// LOOPBACK, which the page names itself.
async function list() {
  if (!supported()) return { ok: false, error: 'unsupported' };
  if (loopback()) return { ok: true, loopback: true, sinks: [{ name: LOOPBACK, description: '', default: true }] };
  try {
    const sinks = parseSinks(await run('pactl', ['list', 'sinks']));
    let fallback = '';
    try {
      fallback = (await run('pactl', ['get-default-sink'])).trim();
    } catch {
      // older pactl: read it from pactl info
      try {
        const m = /^Default Sink:\s*(.*)$/m.exec(await run('pactl', ['info']));
        fallback = m ? m[1].trim() : '';
      } catch {
        // no default then
      }
    }
    return { ok: true, sinks: sinks.map((s) => ({ ...s, default: s.name === fallback })) };
  } catch (err) {
    return { ok: false, error: errorCode(err), message: String((err && err.message) || err) };
  }
}

const validSink = (sink) => typeof sink === 'string' && sink.length > 0 && sink.length < 512 && !/[\0\n]/.test(sink);

// createSystemAudio keeps the running captures; onData(id, chunk) gets each chunk of float
// samples (a Buffer, whole samples), onEnd(id) is called when a capture stops by itself.
function createSystemAudio({ onData, onEnd }) {
  const captures = new Map();
  let nextId = 1;
  // Until when the page may have a loopback capture (0: not now).
  let loopbackUntil = 0;

  // start records the monitor of the output named sink at rate samples per second:
  // {ok, id} or {ok: false, error, message}. Where Chromium records, it only lets the page
  // capture what the computer plays: {ok, loopback: true}.
  function start(sink, rate) {
    if (!supported()) return Promise.resolve({ ok: false, error: 'unsupported' });
    if (loopback()) {
      if (sink !== LOOPBACK) return Promise.resolve({ ok: false, error: 'failed', message: 'invalid output' });
      loopbackUntil = Date.now() + loopbackGrace;
      return Promise.resolve({ ok: true, loopback: true });
    }
    if (!validSink(sink)) return Promise.resolve({ ok: false, error: 'failed', message: 'invalid output' });
    const sampleRate = Number.isInteger(rate) && rate >= 8_000 && rate <= 192_000 ? rate : 48_000;
    const id = nextId++;
    return new Promise((resolve) => {
      let child;
      try {
        child = spawn(
          'parec',
          [`--device=${sink}.monitor`, '--format=float32le', `--rate=${sampleRate}`, '--channels=1', '--latency-msec=40', '--client-name=knowpod', '--stream-name=Meeting recording'],
          { env: env(), stdio: ['ignore', 'pipe', 'pipe'] },
        );
      } catch (err) {
        resolve({ ok: false, error: errorCode(err), message: String(err.message || err) });
        return;
      }
      let answered = false;
      let started = false;
      let stderr = '';
      // parec writes whole samples, but pipes may split them: keep the odd bytes for the next chunk.
      let rest = Buffer.alloc(0);
      const answer = (result) => {
        if (answered) return;
        answered = true;
        resolve(result);
      };
      captures.set(id, child);
      child.stderr.on('data', (d) => {
        stderr = (stderr + d).slice(-2_000);
      });
      child.stdout.on('data', (chunk) => {
        if (!answered) started = true;
        answer({ ok: true, id });
        const data = rest.length ? Buffer.concat([rest, chunk]) : chunk;
        const whole = data.length - (data.length % 4);
        rest = data.subarray(whole);
        if (whole > 0) onData(id, data.subarray(0, whole));
      });
      child.on('error', (err) => {
        captures.delete(id);
        answer({ ok: false, error: errorCode(err), message: String(err.message || err) });
      });
      child.on('exit', (code) => {
        const ours = captures.delete(id);
        answer({ ok: false, error: errorCode({ message: stderr }), message: stderr.trim() || `parec exited (${code})` });
        if (ours && started) onEnd(id);
      });
      // A monitor delivers silence too, so data comes at once; if it doesn't, say it started anyway.
      setTimeout(() => {
        if (!answered) started = true;
        answer({ ok: true, id });
      }, 1_500);
    });
  }

  function stop(id) {
    const child = captures.get(id);
    if (!child) return;
    captures.delete(id);
    child.kill();
  }

  function stopAll() {
    loopbackUntil = 0;
    for (const id of [...captures.keys()]) stop(id);
  }

  // takeLoopback says whether a screen capture asked for now may have what the computer
  // plays: once per start, within loopbackGrace.
  function takeLoopback() {
    const granted = loopback() && Date.now() < loopbackUntil;
    loopbackUntil = 0;
    return granted;
  }

  return { list, start, stop, stopAll, takeLoopback, supported };
}

module.exports = { createSystemAudio, parseSinks, supported, loopback };
