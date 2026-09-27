// Runs in every page of the window with no Node access (sandboxed). It tells the web app it
// runs in the desktop app (window.knowpodDesktop), lets it show notifications and open the
// page of a clicked one (see frontend/src/lib/desktop.ts), and gives the setup page its call.
const { contextBridge, ipcRenderer } = require('electron');

const versionArg = process.argv.find((a) => a.startsWith('--knowpod-version='));

contextBridge.exposeInMainWorld('knowpodDesktop', {
  platform: process.platform,
  version: versionArg ? versionArg.slice('--knowpod-version='.length) : '',
  setServer: (url) => ipcRenderer.invoke('knowpod:set-server', url),
  notify: (message) => ipcRenderer.send('knowpod:notify', message),
  onOpen: (listener) => {
    const handler = (_event, url) => listener(url);
    ipcRenderer.on('knowpod:open', handler);
    return () => ipcRenderer.removeListener('knowpod:open', handler);
  },
});
