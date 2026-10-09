// Runs in every page of the window with no Node access (sandboxed). It tells the web app it
// runs in the desktop app (window.knowpodDesktop), lets it show notifications and open the
// page of a clicked one (see frontend/src/lib/desktop.ts), set up the Pocket recorder's
// Bluetooth connection and pair the knowpod recorder, record what the computer plays (Linux), and
// gives the setup page its call.
// It also fits the window's title bar to the page (see titlebar.css).
const { contextBridge, ipcRenderer, webFrame } = require('electron');

const versionArg = process.argv.find((a) => a.startsWith('--knowpod-version='));

contextBridge.exposeInMainWorld('knowpodDesktop', {
  platform: process.platform,
  version: versionArg ? versionArg.slice('--knowpod-version='.length) : '',
  setServer: (url) => ipcRenderer.invoke('knowpod:set-server', url),
  notify: (message) => ipcRenderer.send('knowpod:notify', message),
  // pocketBluetooth sets up the Pocket recorder's Bluetooth connection, which switches its
  // USB drive on (see pocket-bluetooth.js) and drives the copy over its WiFi
  // (pocket-wifi-sync.js): request is {action: 'settings' | 'save' | 'check' | 'usb-on' |
  // 'state' | 'sync' | 'eject' | 'wifi-sync' | 'wifi-cancel', address?, sessionKey?}.
  pocketBluetooth: (request) => ipcRenderer.invoke('knowpod:pocket-bluetooth', request),
  // systemAudio records what the computer plays, for the meeting recorder (Linux, Windows and
  // macOS, see system-audio.js): request is {action: 'list'} | {action: 'start', sink, rate} |
  // {action: 'stop', id}. onSystemAudio gets the recordings' chunks of float samples (Linux)
  // and hears when one stops by itself.
  systemAudio: ['linux', 'win32', 'darwin'].includes(process.platform) ? (request) => ipcRenderer.invoke('knowpod:system-audio', request) : undefined,
  onSystemAudio: (onData, onEnd) => {
    const data = (_event, id, chunk) => onData(id, chunk);
    const end = (_event, id) => onEnd(id);
    ipcRenderer.on('knowpod:system-audio-data', data);
    ipcRenderer.on('knowpod:system-audio-end', end);
    return () => {
      ipcRenderer.removeListener('knowpod:system-audio-data', data);
      ipcRenderer.removeListener('knowpod:system-audio-end', end);
    };
  },
  // recorderBluetooth pairs the knowpod recorder (ESP32) and copies its recordings over
  // Bluetooth while it has no Wi-Fi (see recorder-bluetooth.js): request is {action: 'state' |
  // 'pair' | 'pin' | 'sync' | 'cancel' | 'enable' | 'forget', name?, pin?, enabled?}.
  recorderBluetooth: (request) => ipcRenderer.invoke('knowpod:recorder-bluetooth', request),
  onOpen: (listener) => {
    const handler = (_event, url) => listener(url);
    ipcRenderer.on('knowpod:open', handler);
    return () => ipcRenderer.removeListener('knowpod:open', handler);
  },
});

// The window has no title bar: the page's header takes its place (titlebar.css). The
// window's buttons are drawn over it on Windows and Linux, so they get the header's colors
// and the height of its first row; pages without the header get a strip in their own
// background. Sent again whenever the page, its theme or the zoom changes.
const noHeaderHeight = 36; // the .knowpod-titlebar strip

// hex is a computed color ("rgb(30, 42, 58)") as #rrggbb, or null if it's transparent.
function hex(color) {
  const m = /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/.exec(color || '');
  if (!m || (m[4] !== undefined && Number(m[4]) < 0.5)) return null;
  return `#${m.slice(1, 4).map((n) => Number(n).toString(16).padStart(2, '0')).join('')}`;
}

let sentStyle = '';

function sendTitleBar() {
  const header = document.querySelector('.header');
  const root = getComputedStyle(document.documentElement);
  const body = document.body ? getComputedStyle(document.body) : root;
  let height = noHeaderHeight;
  let color = hex(body.backgroundColor) || hex(root.backgroundColor);
  let symbolColor = hex(body.color);
  if (header) {
    // The first row: the header's items are centered in it, the brand among them.
    const box = header.getBoundingClientRect();
    const first = header.firstElementChild;
    const row = first ? first.getBoundingClientRect() : null;
    height = row ? 2 * (row.top + row.height / 2 - box.top) : box.height;
    color = hex(getComputedStyle(header).backgroundColor) || color;
    if (first) symbolColor = hex(getComputedStyle(first).color) || symbolColor;
  }
  const style = { height: Math.round(height * webFrame.getZoomFactor()), color, symbolColor };
  const key = JSON.stringify(style);
  if (key === sentStyle) return;
  sentStyle = key;
  ipcRenderer.send('knowpod:titlebar', style);
}

let pending = false;
function scheduleTitleBar() {
  if (pending) return;
  pending = true;
  requestAnimationFrame(() => {
    pending = false;
    sendTitleBar();
  });
}

window.addEventListener('DOMContentLoaded', () => {
  const html = document.documentElement;
  html.dataset.titlebar = process.platform;
  const strip = document.createElement('div');
  strip.className = 'knowpod-titlebar';
  html.appendChild(strip);
  // The header comes and goes with the page; the theme and font size are attributes of <html>.
  new MutationObserver(scheduleTitleBar).observe(html, {
    childList: true,
    subtree: true,
    attributeFilter: ['data-theme', 'data-font-size', 'class', 'style'],
  });
  window.addEventListener('resize', scheduleTitleBar);
  scheduleTitleBar();
});

ipcRenderer.on('knowpod:fullscreen', (_event, on) => {
  if (on) document.documentElement.dataset.fullscreen = '';
  else delete document.documentElement.dataset.fullscreen;
});
