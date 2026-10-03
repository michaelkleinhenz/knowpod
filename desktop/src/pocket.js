// Copies recordings from a Pocket recorder (heypocketai.com) plugged in by USB, so the Pocket
// app isn't needed. The recorder shows up as a drive named "Pocket" that the OS mounts by
// itself; its recordings are MP3 files in RECORD/<date>/, named by when they started in UTC
// (RECORD/2026-10-2/20261002090356.mp3). Electron has no event for new drives, so the mounted
// drives are looked at every few seconds. When a recorder appears, the server is asked which
// of its files aren't notes yet, and those are uploaded; the server puts them into the folder
// "Pocket AI" (see backend/internal/service/pocket.go). Files are only read, never changed.
const { net, session } = require('electron');
const { execFile } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

// How often the mounted drives are looked at.
const pollInterval = 5_000;
// How long to wait before trying again while a recorder stays plugged in after a copy failed
// (offline, signed out, server error).
const retryInterval = 2 * 60_000;
// How many file names the app remembers per server as copied (a few years of recordings).
const maxRemembered = 20_000;

const recordingName = /^\d{14}\.mp3$/i;

// decodeMountPath undoes the escapes of /proc/mounts ("\040" for a space).
const decodeMountPath = (p) => p.replace(/\\([0-7]{3})/g, (_m, octal) => String.fromCharCode(parseInt(octal, 8)));

const isDirectory = (p) => {
  try {
    return fs.statSync(p).isDirectory();
  } catch {
    return false;
  }
};

// recordFolder returns the drive's RECORD folder (any case), or null.
function recordFolder(root) {
  try {
    const name = fs.readdirSync(root).find((n) => n.toUpperCase() === 'RECORD');
    return name && isDirectory(path.join(root, name)) ? path.join(root, name) : null;
  } catch {
    return null;
  }
}

// windowsLabel returns the label of a Windows drive ("E:"), or null.
function windowsLabel(drive) {
  return new Promise((resolve) => {
    execFile(
      'powershell.exe',
      ['-NoProfile', '-NonInteractive', '-Command', `(Get-Volume -DriveLetter ${drive[0]}).FileSystemLabel`],
      { timeout: 10_000, windowsHide: true },
      (err, stdout) => resolve(err ? null : String(stdout).trim()),
    );
  });
}

// Labels of Windows drives, by drive: asking takes a moment, so it's done once per drive
// while it stays plugged in.
const labels = new Map();

// candidates lists the mounted drives that may be a recorder: [{root, name}], name being the
// drive's label (null if it couldn't be read).
async function candidates() {
  if (process.platform === 'win32') {
    const found = [];
    for (const letter of 'DEFGHIJKLMNOPQRSTUVWXYZ') {
      const root = `${letter}:\\`;
      if (!recordFolder(root)) {
        labels.delete(root);
        continue;
      }
      if (!labels.has(root)) labels.set(root, await windowsLabel(root));
      found.push({ root, name: labels.get(root) });
    }
    return found;
  }
  if (process.platform === 'darwin') {
    try {
      return fs.readdirSync('/Volumes').map((name) => ({ root: path.join('/Volumes', name), name }));
    } catch {
      return [];
    }
  }
  // Linux and others: the mount points of /proc/mounts (udisks mounts drives under
  // /media/<user>/<label> or /run/media/<user>/<label>).
  try {
    return fs
      .readFileSync('/proc/mounts', 'utf8')
      .split('\n')
      .map((line) => line.split(' ')[1])
      .filter((p) => p && p !== '/' && !/^\/(proc|sys|dev|run\/user)(\/|$)/.test(p))
      .map((p) => decodeMountPath(p))
      .map((root) => ({ root, name: path.basename(root) }));
  } catch {
    return [];
  }
}

// findRecorders returns the root folders of the plugged-in recorders: drives named "Pocket"
// (also "POCKET" or "Pocket1", as some systems name a second one) with a RECORD folder. A
// drive whose label couldn't be read counts when its RECORD folder holds recordings.
async function findRecorders() {
  const roots = [];
  for (const { root, name } of await candidates()) {
    if (roots.includes(root)) continue; // mounted twice
    if (name === null ? recordings(root).length > 0 : /^pocket/i.test(name) && recordFolder(root)) roots.push(root);
  }
  return roots;
}

// recordings lists the recorder's MP3 files, oldest first: [{name, file, size}].
function recordings(root) {
  const record = recordFolder(root);
  if (!record) return [];
  const found = [];
  const add = (dir) => {
    let entries = [];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (entry.isDirectory() && dir === record) add(path.join(dir, entry.name));
      else if (entry.isFile() && recordingName.test(entry.name)) {
        const file = path.join(dir, entry.name);
        try {
          const size = fs.statSync(file).size;
          if (size > 0) found.push({ name: entry.name, file, size });
        } catch {
          // gone, e.g. unplugged
        }
      }
    }
  };
  add(record);
  return found.sort((a, b) => a.name.localeCompare(b.name));
}

class HttpError extends Error {
  constructor(status, body) {
    let message = `HTTP ${status}`;
    try {
      message = JSON.parse(body).message || message;
    } catch {
      // not JSON
    }
    super(message);
    this.status = status;
  }
}

// request calls the server as the signed-in user (the window's session cookie). The body is
// a string, or a file to stream.
function request(method, url, { headers = {}, body, file } = {}) {
  return new Promise((resolve, reject) => {
    const req = net.request({ method, url, session: session.defaultSession, useSessionCookies: true, credentials: 'include' });
    for (const [name, value] of Object.entries(headers)) req.setHeader(name, value);
    req.on('response', (res) => {
      const chunks = [];
      res.on('data', (chunk) => chunks.push(chunk));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        if (res.statusCode >= 200 && res.statusCode < 300) {
          try {
            resolve(text ? JSON.parse(text) : {});
          } catch {
            resolve({});
          }
        } else reject(new HttpError(res.statusCode, text));
      });
      res.on('error', reject);
    });
    req.on('error', reject);
    if (file) {
      // Streamed, not read into memory: recordings can be hours long.
      req.chunkedEncoding = true;
      const stream = fs.createReadStream(file);
      stream.on('error', (err) => {
        req.abort();
        reject(err);
      });
      stream.pipe(req);
    } else {
      req.end(body);
    }
  });
}

// startPocketSync watches for recorders and copies their new recordings to the server.
// It returns {state(), syncNow()} for the tray menu. options:
//   serverUrl()            the server, or null
//   readConfig(), writeConfig(config)
//   notify({title, body, url, tag})
//   onChange()             called when state() changed
function startPocketSync({ serverUrl, readConfig, writeConfig, notify, onChange }) {
  const enabled = () => readConfig().pocketSync !== false;
  let mounted = []; // roots of the plugged-in recorders
  let syncing = false;
  let failedAt = 0; // when the last copy failed, while the recorder stays plugged in
  let failureShown = false; // a failure is told once per plug-in, not on every retry
  let status = ''; // shown in the tray menu
  // progress is the state for the web app's Pocket USB Sync dialog: phase is '' (nothing
  // yet), 'checking', 'copying' (current of total), 'done' (copied this time), 'signed-out'
  // or 'failed'.
  let progress = { phase: '', current: 0, total: 0, copied: 0 };

  const setStatus = (text, next) => {
    status = text;
    if (next) progress = { ...progress, ...next };
    onChange();
  };

  // remembered are the files already copied to the server: they aren't asked about again,
  // so a note deleted for good isn't copied again either.
  const rememberedKey = (server) => `pocketCopied:${server}`;
  const remembered = (server) => new Set(readConfig()[rememberedKey(server)] || []);
  const remember = (server, name) => {
    const config = readConfig();
    const list = (config[rememberedKey(server)] || []).filter((n) => n !== name);
    list.push(name);
    writeConfig({ ...config, [rememberedKey(server)]: list.slice(-maxRemembered) });
  };

  async function sync(roots) {
    const server = serverUrl();
    if (!server || syncing) return;
    syncing = true;
    let copied = 0;
    let lastId = '';
    try {
      const known = remembered(server);
      // Each file once, also when the recorder is mounted twice (Set.add returns the set).
      const files = roots.flatMap(recordings).filter((f) => !known.has(f.name) && known.add(f.name));
      if (!files.length) {
        setStatus('Pocket: all recordings copied', { phase: 'done', copied: 0 });
        return;
      }
      setStatus('Pocket: checking recordings…', { phase: 'checking' });
      const { files: fresh = [] } = await request('POST', `${server}/api/v1/me/pocket/device/check`, {
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ files: files.map((f) => f.name) }),
      });
      const wanted = new Set(fresh);
      for (const f of files) if (!wanted.has(f.name)) remember(server, f.name);
      const todo = files.filter((f) => wanted.has(f.name));
      if (!todo.length) {
        setStatus('Pocket: all recordings copied', { phase: 'done', copied: 0 });
        return;
      }
      notify({
        title: 'Copying from Pocket',
        body: todo.length === 1 ? 'Copying 1 new recording to knowpod…' : `Copying ${todo.length} new recordings to knowpod…`,
        tag: 'pocket-sync',
      });
      for (const [i, f] of todo.entries()) {
        setStatus(`Pocket: copying ${i + 1} of ${todo.length}…`, { phase: 'copying', current: i + 1, total: todo.length });
        try {
          const rec = await request('POST', `${server}/api/v1/me/pocket/device/files`, {
            headers: { 'Content-Type': 'audio/mpeg', 'X-Filename': encodeURIComponent(f.name) },
            file: f.file,
          });
          copied++;
          lastId = rec.id || lastId;
        } catch (err) {
          if (!(err instanceof HttpError && err.status === 409)) throw err; // 409: already a note
        }
        remember(server, f.name);
      }
      failedAt = 0;
      failureShown = false;
      setStatus('Pocket: all recordings copied', { phase: 'done', copied });
      if (!copied) return; // all were notes by now
      notify({
        title: 'Copied from Pocket',
        body: `${copied === 1 ? '1 new recording is' : `${copied} new recordings are`} in the folder "Pocket AI" and will be transcribed and summarized.`,
        url: copied === 1 && lastId ? `/conversations/${lastId}` : '/',
        tag: 'pocket-sync',
      });
    } catch (err) {
      failedAt = Date.now();
      const signedOut = err instanceof HttpError && err.status === 401;
      const reason = signedOut ? 'Sign in to knowpod to copy them.' : err.message;
      setStatus(signedOut ? 'Pocket: sign in to copy recordings' : 'Pocket: copying failed, trying again soon', {
        phase: signedOut ? 'signed-out' : 'failed',
        copied,
      });
      // Unplugged while copying: nothing to tell.
      if (!failureShown && roots.some((root) => recordFolder(root))) {
        failureShown = true;
        notify({
          title: "Couldn't copy from Pocket",
          body: `${copied ? `${copied} copied. ` : ''}${reason}`,
          url: '/',
          tag: 'pocket-sync',
        });
      }
      console.error('pocket sync:', err);
    } finally {
      syncing = false;
      onChange();
    }
  }

  let polling = false;
  async function poll() {
    if (polling) return;
    polling = true;
    try {
      const roots = await findRecorders();
      const added = roots.filter((r) => !mounted.includes(r));
      const changed = added.length || roots.length !== mounted.length;
      mounted = roots;
      if (!roots.length) {
        failedAt = 0;
        failureShown = false;
        if (changed) setStatus('', { phase: '', current: 0, total: 0, copied: 0 });
        return;
      }
      if (changed) onChange();
      if (!enabled()) return;
      if (added.length || (failedAt && Date.now() - failedAt >= retryInterval)) await sync(roots);
    } catch (err) {
      console.error('pocket watch:', err);
    } finally {
      polling = false;
    }
  }

  setInterval(() => void poll(), pollInterval);
  void poll();

  return {
    // state is what the tray menu shows.
    state: () => ({ enabled: enabled(), connected: mounted.length > 0, syncing, status, ...progress }),
    setEnabled: (on) => {
      writeConfig({ ...readConfig(), pocketSync: on });
      onChange();
      if (on && mounted.length) void sync(mounted);
    },
    // lookNow looks for plugged-in recorders right away, e.g. after their USB drive was
    // switched on.
    lookNow: () => void poll(),
    syncNow: () => {
      failureShown = false;
      if (mounted.length) void sync(mounted);
    },
  };
}

module.exports = { startPocketSync, findRecorders, recordings };
