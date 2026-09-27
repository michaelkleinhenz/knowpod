// Runs in every page of the window with no Node access (sandboxed). It tells the web app it
// runs in the desktop app (window.knowpodDesktop) and gives the setup page its one call.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('knowpodDesktop', {
  platform: process.platform,
  setServer: (url) => ipcRenderer.invoke('knowpod:set-server', url),
});
