// knowpod desktop: a native window around the knowpod web app. The backend needs MongoDB and
// S3, so it isn't bundled; the app loads the web UI from a knowpod server instead, like the
// installed web app (PWA) does. The server's address is asked for on the first start (or
// baked in at build time, see README) and kept in the user's app data folder.
const { app, BrowserWindow, Menu, dialog, ipcMain, shell } = require('electron');
const fs = require('node:fs');
const path = require('node:path');

const pkg = require('../package.json');

const configFile = () => path.join(app.getPath('userData'), 'config.json');

function readConfig() {
  try {
    return JSON.parse(fs.readFileSync(configFile(), 'utf8'));
  } catch {
    return {};
  }
}

function writeConfig(config) {
  fs.mkdirSync(path.dirname(configFile()), { recursive: true });
  fs.writeFileSync(configFile(), JSON.stringify(config, null, 2));
}

// normalizeServerUrl makes an origin of what the user typed ("knowpod.example.com" →
// "https://knowpod.example.com"), or returns null if it isn't an http(s) address.
function normalizeServerUrl(input) {
  let text = String(input || '').trim();
  if (!text) return null;
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) text = `https://${text}`;
  try {
    const url = new URL(text);
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return null;
    return url.origin;
  } catch {
    return null;
  }
}

// serverUrl is the server to load: KNOWPOD_SERVER_URL, else the saved one, else the one
// baked in at build time.
function serverUrl() {
  return (
    normalizeServerUrl(process.env.KNOWPOD_SERVER_URL) ||
    normalizeServerUrl(readConfig().serverUrl) ||
    normalizeServerUrl(pkg.defaultServerUrl)
  );
}

let mainWindow = null;

// isAppUrl says whether a link stays in the app window: pages of the server itself.
function isAppUrl(target) {
  const server = serverUrl();
  try {
    return !!server && new URL(target).origin === server;
  } catch {
    return false;
  }
}

function saveBounds() {
  if (!mainWindow || mainWindow.isMinimized()) return;
  const config = readConfig();
  config.bounds = mainWindow.getNormalBounds();
  config.maximized = mainWindow.isMaximized();
  writeConfig(config);
}

function createWindow() {
  const config = readConfig();
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 360,
    minHeight: 480,
    ...(config.bounds || {}),
    title: 'knowpod',
    backgroundColor: '#eef1f5',
    icon: path.join(__dirname, '..', 'build', 'icon.png'),
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      spellcheck: true,
    },
  });
  if (config.maximized) mainWindow.maximize();
  mainWindow.once('ready-to-show', () => mainWindow.show());
  mainWindow.on('close', saveBounds);
  mainWindow.on('closed', () => {
    mainWindow = null;
  });

  // Links to other sites open in the default browser; the app's own pages stay in the window.
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (isAppUrl(url)) return { action: 'allow' };
    if (/^(https?|mailto):/i.test(url)) void shell.openExternal(url);
    return { action: 'deny' };
  });
  mainWindow.webContents.on('will-navigate', (event, url) => {
    if (isAppUrl(url) || url.startsWith('file:')) return;
    event.preventDefault();
    if (/^(https?|mailto):/i.test(url)) void shell.openExternal(url);
  });

  // A server that can't be reached (and has no copy of the app cached yet) shows the setup
  // page with the error, so the address can be corrected.
  mainWindow.webContents.on('did-fail-load', (_event, code, description, url, isMainFrame) => {
    if (!isMainFrame || code === -3 /* ERR_ABORTED */ || url.startsWith('file:')) return;
    showSetup(`${description} (${url})`);
  });

  load();
}

function load() {
  const server = serverUrl();
  if (server) void mainWindow.loadURL(server);
  else showSetup();
}

function showSetup(error) {
  if (!mainWindow) return;
  const query = { current: serverUrl() || '' };
  if (error) query.error = error;
  void mainWindow.loadFile(path.join(__dirname, 'setup.html'), { query });
}

ipcMain.handle('knowpod:set-server', (event, input) => {
  // Only the bundled setup page may change the server, never a page the server sent.
  if (!event.senderFrame?.url.startsWith('file:')) return { ok: false };
  const url = normalizeServerUrl(input);
  if (!url) return { ok: false };
  writeConfig({ ...readConfig(), serverUrl: url });
  load();
  return { ok: true, url };
});

function buildMenu() {
  const isMac = process.platform === 'darwin';
  const serverItem = {
    label: 'Change Server…',
    click: () => showSetup(),
  };
  const template = [
    ...(isMac
      ? [
          {
            label: app.name,
            submenu: [{ role: 'about' }, { type: 'separator' }, serverItem, { type: 'separator' }, { role: 'services' }, { type: 'separator' }, { role: 'hide' }, { role: 'hideOthers' }, { role: 'unhide' }, { type: 'separator' }, { role: 'quit' }],
          },
        ]
      : [{ label: 'File', submenu: [serverItem, { type: 'separator' }, { role: 'quit' }] }]),
    { role: 'editMenu' },
    {
      label: 'View',
      submenu: [
        { label: 'Back', accelerator: isMac ? 'Cmd+[' : 'Alt+Left', click: () => mainWindow?.webContents.navigationHistory.goBack() },
        { label: 'Forward', accelerator: isMac ? 'Cmd+]' : 'Alt+Right', click: () => mainWindow?.webContents.navigationHistory.goForward() },
        { type: 'separator' },
        { role: 'reload' },
        { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' },
        { role: 'zoomIn' },
        { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    { role: 'windowMenu' },
    {
      role: 'help',
      submenu: [
        {
          label: 'Open in Browser',
          click: () => {
            const url = mainWindow?.webContents.getURL();
            if (url && isAppUrl(url)) void shell.openExternal(url);
            else if (serverUrl()) void shell.openExternal(serverUrl());
          },
        },
        { label: 'knowpod on GitHub', click: () => void shell.openExternal(pkg.homepage) },
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

// One window only: starting the app again brings the running one to the front.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  app.whenReady().then(() => {
    app.setAboutPanelOptions({ applicationName: 'knowpod', applicationVersion: app.getVersion() });
    buildMenu();
    createWindow();
    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
}

process.on('uncaughtException', (err) => {
  dialog.showErrorBox('knowpod', String(err?.stack || err));
});
