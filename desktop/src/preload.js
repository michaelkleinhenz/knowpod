// Runs in every page of the window with no Node access (sandboxed). It tells the web app it
// runs in the desktop app (window.knowpodDesktop) and gives the setup page its one call.
const { contextBridge, ipcRenderer } = require('electron');

const versionArg = process.argv.find((a) => a.startsWith('--knowpod-version='));

contextBridge.exposeInMainWorld('knowpodDesktop', {
  platform: process.platform,
  version: versionArg ? versionArg.slice('--knowpod-version='.length) : '',
  setServer: (url) => ipcRenderer.invoke('knowpod:set-server', url),
});
