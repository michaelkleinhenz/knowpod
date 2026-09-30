// knowpod desktop: a native window around the knowpod web app. The backend needs MongoDB and
// S3, so it isn't bundled; the app loads the web UI from a knowpod server instead, like the
// installed web app (PWA) does. The server's address is asked for on the first start (or
// baked in at build time, see README) and kept in the user's app data folder.
//
// Web Push can't reach Electron (it has no push service), so the web app listens to the
// server's live notification stream and hands each notification to notify() here. To get
// them also while the window is closed, closing it only hides it: the app keeps running in
// the tray (the menu bar on macOS) until Quit.
const { app, BrowserWindow, Menu, Notification, Tray, dialog, ipcMain, nativeImage, powerMonitor, shell } = require('electron');
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
let tray = null;
// quitting is set once the app is really quitting, so closing the window doesn't just hide it.
let quitting = false;

const iconPath = path.join(__dirname, '..', 'build', 'icon.png');
const isMac = process.platform === 'darwin';

// The window has no title bar: the web app's header takes its place, like in VS Code (see
// titlebar.css, loaded into every page). The window's buttons stay: macOS draws its traffic
// lights over the header's left end, Windows and Linux draw minimize, maximize and close over
// its right end (the window controls overlay). The page reports its header's colors and
// height (preload.js), which these start with (the header's, light theme).
const titleBarCss = fs.readFileSync(path.join(__dirname, 'titlebar.css'), 'utf8');
let titleBar = { height: 50, color: '#1e2a3a', symbolColor: '#e6ebf2' };
const trafficLightPosition = (height) => ({ x: 18, y: Math.max(0, Math.round((height - 16) / 2)) });

// runInBackground says whether closing the window keeps the app running in the tray (on by
// default; switched in the tray menu).
const runInBackground = () => readConfig().background !== false;

// startedHidden says whether the app was started at login, where it starts in the tray.
const startedHidden = () => process.argv.includes('--hidden') || !!app.getLoginItemSettings().wasOpenedAsHidden;

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
    icon: iconPath,
    show: false,
    // No title bar and so no menu bar in the window on Windows and Linux: the web app has its
    // own navigation. Alt opens the menu (see popUpMenuOnAlt); its shortcuts keep working.
    // macOS keeps its menu at the top of the screen.
    titleBarStyle: 'hidden',
    ...(isMac ? { trafficLightPosition: trafficLightPosition(titleBar.height) } : { titleBarOverlay: titleBar }),
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      spellcheck: true,
      additionalArguments: [`--knowpod-version=${app.getVersion()}`],
    },
  });
  if (config.maximized) mainWindow.maximize();
  const hidden = startedHidden() && runInBackground();
  mainWindow.once('ready-to-show', () => {
    if (!hidden) mainWindow.show();
  });
  mainWindow.on('close', (event) => {
    saveBounds();
    if (quitting || !runInBackground()) return;
    // Keep the page (and with it the notification stream) running; only hide the window.
    event.preventDefault();
    mainWindow.hide();
    hintBackground();
  });
  // Logging off or shutting down Windows closes the window; hiding it would hold that up.
  mainWindow.on('query-session-end', () => {
    quitting = true;
  });
  mainWindow.on('closed', () => {
    mainWindow = null;
  });
  mainWindow.on('enter-full-screen', () => mainWindow.webContents.send('knowpod:fullscreen', true));
  mainWindow.on('leave-full-screen', () => mainWindow.webContents.send('knowpod:fullscreen', false));
  mainWindow.webContents.on('dom-ready', () => {
    void mainWindow.webContents.insertCSS(titleBarCss);
    if (mainWindow.isFullScreen()) mainWindow.webContents.send('knowpod:fullscreen', true);
  });
  if (!isMac) popUpMenuOnAlt(mainWindow);

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

// setTitleBar fits the window's buttons to the page's header: {height, color, symbolColor}.
function setTitleBar(style) {
  if (!mainWindow) return;
  const color = (c) => (typeof c === 'string' && /^#[0-9a-f]{6}$/i.test(c) ? c : null);
  const height = Number.isFinite(style.height) ? Math.min(100, Math.max(24, Math.round(style.height))) : titleBar.height;
  titleBar = {
    height,
    color: color(style.color) || titleBar.color,
    symbolColor: color(style.symbolColor) || titleBar.symbolColor,
  };
  if (isMac) mainWindow.setWindowButtonPosition(trafficLightPosition(height));
  else mainWindow.setTitleBarOverlay(titleBar);
}

ipcMain.on('knowpod:titlebar', (event, style) => {
  if (!mainWindow || event.sender !== mainWindow.webContents || !style || typeof style !== 'object') return;
  setTitleBar(style);
});

// popUpMenuOnAlt opens the window's menu below the header when Alt is pressed and released
// on its own (not as part of a shortcut like Alt+Left), as Alt shows a hidden menu bar.
function popUpMenuOnAlt(window) {
  let altAlone = false;
  window.webContents.on('before-input-event', (_event, input) => {
    if (input.key !== 'Alt') {
      if (input.type === 'keyDown') altAlone = false;
      return;
    }
    if (input.type === 'keyDown') {
      if (!input.isAutoRepeat) altAlone = true;
    } else if (input.type === 'keyUp' && altAlone) {
      altAlone = false;
      Menu.getApplicationMenu()?.popup({ window, x: 0, y: titleBar.height });
    }
  });
  window.on('blur', () => {
    altAlone = false;
  });
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

// showWindow brings the window to the front, making one if there is none.
function showWindow() {
  if (!mainWindow) createWindow();
  if (mainWindow.isMinimized()) mainWindow.restore();
  mainWindow.show();
  mainWindow.focus();
}

// hintBackground tells once that the app keeps running after its window was closed.
function hintBackground() {
  const config = readConfig();
  if (config.backgroundHintShown || !Notification.isSupported()) return;
  writeConfig({ ...config, backgroundHintShown: true });
  const where = process.platform === 'darwin' ? 'menu bar' : 'tray';
  showNotification({
    title: 'knowpod is still running',
    body: `It keeps showing your reminders. To quit, use the knowpod icon in the ${where}.`,
  });
}

// notifications are the ones on screen, by tag; a newer one with the same tag replaces the
// older. Keeping them referenced keeps their click handlers alive.
const notifications = new Map();
let untagged = 0;

// showNotification shows a notification {title, body, url, tag} from the server (see
// backend/internal/service/notifications.go). Clicking it opens its page in the window.
function showNotification(message) {
  if (!Notification.isSupported()) return;
  const tag = typeof message.tag === 'string' && message.tag ? message.tag : `untagged-${untagged++}`;
  notifications.get(tag)?.close();
  const notification = new Notification({
    title: String(message.title || 'knowpod').slice(0, 200),
    body: String(message.body || '').slice(0, 500),
    icon: iconPath,
  });
  const forget = () => {
    if (notifications.get(tag) === notification) notifications.delete(tag);
  };
  notification.on('click', () => {
    forget();
    showWindow();
    if (typeof message.url === 'string' && message.url.startsWith('/')) openInApp(message.url);
  });
  notification.on('close', forget);
  notifications.set(tag, notification);
  notification.show();
}

// openInApp shows a page of the server: the web app navigates itself (see
// frontend/src/lib/desktop.ts), unless the window shows something else, e.g. the setup page.
function openInApp(pathname) {
  const server = serverUrl();
  if (!server || !mainWindow) return;
  const target = new URL(pathname, server).href;
  if (!isAppUrl(target)) return; // e.g. "//elsewhere.example"
  if (isAppUrl(mainWindow.webContents.getURL())) mainWindow.webContents.send('knowpod:open', target);
  else void mainWindow.loadURL(target);
}

// Only pages of the server may show notifications.
ipcMain.on('knowpod:notify', (event, message) => {
  if (!event.senderFrame || !isAppUrl(event.senderFrame.url) || !message || typeof message !== 'object') return;
  showNotification(message);
});

ipcMain.handle('knowpod:set-server', (event, input) => {
  // Only the bundled setup page may change the server, never a page the server sent.
  if (!event.senderFrame?.url.startsWith('file:')) return { ok: false };
  const url = normalizeServerUrl(input);
  if (!url) return { ok: false };
  writeConfig({ ...readConfig(), serverUrl: url });
  load();
  return { ok: true, url };
});

// Start at login: Windows and macOS keep the setting themselves; on Linux it's an autostart
// entry (XDG), which is read back to show the setting.
const autostartFile = () => path.join(app.getPath('appData'), 'autostart', 'knowpod.desktop');

function openAtLogin() {
  if (process.platform === 'linux') return fs.existsSync(autostartFile());
  return app.getLoginItemSettings({ args: ['--hidden'] }).openAtLogin;
}

function setOpenAtLogin(on) {
  if (process.platform !== 'linux') {
    app.setLoginItemSettings({ openAtLogin: on, openAsHidden: on, args: ['--hidden'] });
    return;
  }
  if (!on) {
    fs.rmSync(autostartFile(), { force: true });
    return;
  }
  // An AppImage runs from a temporary mount; the file itself is in APPIMAGE.
  const exec = process.env.APPIMAGE || process.execPath;
  fs.mkdirSync(path.dirname(autostartFile()), { recursive: true });
  fs.writeFileSync(
    autostartFile(),
    ['[Desktop Entry]', 'Type=Application', 'Name=knowpod', `Exec="${exec.replace(/(["\\`$])/g, '\\$1')}" --hidden`, 'X-GNOME-Autostart-enabled=true', ''].join('\n'),
  );
}

// startAtLoginAvailable says whether the app can start itself at login: not when run from
// source (it would start Electron without the app).
const startAtLoginAvailable = () => app.isPackaged;

function buildTrayMenu() {
  return Menu.buildFromTemplate([
    { label: 'Open knowpod', click: showWindow },
    { type: 'separator' },
    {
      label: 'Keep Running When Closed',
      type: 'checkbox',
      checked: runInBackground(),
      click: (item) => {
        writeConfig({ ...readConfig(), background: item.checked });
        updateTray();
      },
    },
    ...(startAtLoginAvailable()
      ? [
          {
            label: 'Start at Login',
            type: 'checkbox',
            checked: openAtLogin(),
            click: (item) => {
              try {
                setOpenAtLogin(item.checked);
              } catch (err) {
                dialog.showErrorBox('knowpod', `Couldn't change the setting: ${err.message}`);
              }
              updateTray();
            },
          },
        ]
      : []),
    { type: 'separator' },
    { label: 'Quit knowpod', click: () => app.quit() },
  ]);
}

// createTray puts the knowpod icon in the tray (the menu bar on macOS), with the menu to open
// the window, change the background settings and quit.
function createTray() {
  const size = process.platform === 'darwin' ? 18 : process.platform === 'win32' ? 16 : 24;
  const image = nativeImage.createFromPath(iconPath).resize({ width: size, height: size, quality: 'best' });
  tray = new Tray(image);
  tray.setToolTip('knowpod');
  updateTray();
  // Windows and most Linux desktops open the window on a click; macOS shows the menu.
  if (process.platform !== 'darwin') tray.on('click', showWindow);
}

function updateTray() {
  tray?.setContextMenu(buildTrayMenu());
}

function buildMenu() {
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
    isMac
      ? { role: 'windowMenu' }
      : {
          label: 'Window',
          submenu: [
            { role: 'minimize' },
            {
              label: 'Maximize / Restore',
              click: () => {
                if (!mainWindow) return;
                if (mainWindow.isMaximized()) mainWindow.unmaximize();
                else mainWindow.maximize();
              },
            },
            { type: 'separator' },
            { role: 'close' },
          ],
        },
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
  app.on('second-instance', showWindow);

  // Windows shows notifications only for an app with an ID (the installer's shortcut has it).
  if (process.platform === 'win32') app.setAppUserModelId(pkg.build.appId);

  app.whenReady().then(() => {
    app.setAboutPanelOptions({ applicationName: 'knowpod', applicationVersion: app.getVersion() });
    buildMenu();
    createTray();
    createWindow();
    // macOS also activates the app when it launches; started at login, it stays in the tray.
    let skipActivate = startedHidden();
    app.on('activate', () => {
      if (skipActivate) skipActivate = false;
      else showWindow();
    });
    // Shutting down Linux or macOS: quit, don't hide.
    powerMonitor.on('shutdown', () => {
      quitting = true;
      app.quit();
    });
  });

  app.on('before-quit', () => {
    quitting = true;
  });

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });
}

process.on('uncaughtException', (err) => {
  dialog.showErrorBox('knowpod', String(err?.stack || err));
});
