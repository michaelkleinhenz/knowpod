// The app version, from the VERSION file at the repository root (set by vite.config.ts).
declare const __APP_VERSION__: string;

export const APP_VERSION = __APP_VERSION__;

type AppWindow = { knowpodDesktop?: { version?: string }; knowpodAndroid?: { version?: string } };

// desktopVersion is the version of the desktop app (Electron, see desktop/) or Android app
// (see mobile/) this page runs in, or '' in a browser. It can differ from APP_VERSION: the
// apps load the web app from the server.
export const desktopVersion = (): string =>
  (typeof window !== 'undefined' && ((window as AppWindow).knowpodDesktop?.version || (window as AppWindow).knowpodAndroid?.version)) || '';
