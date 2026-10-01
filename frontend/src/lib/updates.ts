/// <reference types="vite-plugin-pwa/client" />
import { registerSW } from 'virtual:pwa-register';

// The service worker (see vite.config.ts) starts the app from its cached copy, so a new
// version from the server is only fetched in the background and runs from the next start.
// A window that is never reloaded, like the desktop app's (it keeps running in the tray when
// closed), would run the old version for good. So: look for a new version every half hour
// and whenever the app comes back to the front, and switch to it right away while the app
// has only just started, else as soon as it is in the background, so it never reloads under
// someone's hands.

const CHECK_EVERY = 30 * 60 * 1000;
const START_GRACE = 15 * 1000;
const startedAt = Date.now();

let reloadPending = false;

const reload = () => window.location.reload();

const reloadWhenHidden = () => {
  if (reloadPending) return;
  reloadPending = true;
  if (document.visibilityState === 'hidden') return reload();
  document.addEventListener('visibilitychange', () => document.visibilityState === 'hidden' && reload());
};

if ('serviceWorker' in navigator) {
  registerSW({
    immediate: true,
    onNeedReload: () => (Date.now() - startedAt < START_GRACE ? reload() : reloadWhenHidden()),
    onRegisteredSW: (_url, registration) => {
      if (!registration) return;
      const check = () => {
        if (navigator.onLine && !reloadPending) void registration.update().catch(() => undefined);
      };
      window.setInterval(check, CHECK_EVERY);
      document.addEventListener('visibilitychange', () => document.visibilityState === 'visible' && check());
    },
  });
}
