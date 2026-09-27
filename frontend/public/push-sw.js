// Push notifications for the installed app, imported into the generated service worker
// (see vite.config.ts). The server sends {title, body, url, tag} (see
// backend/internal/service/notifications.go).
/* eslint-env serviceworker */

self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = { body: event.data ? event.data.text() : '' };
  }
  event.waitUntil(
    self.registration.showNotification(data.title || 'knowpod', {
      body: data.body || '',
      tag: data.tag || undefined,
      renotify: !!data.tag,
      icon: '/pwa-192.png',
      badge: '/pwa-192.png',
      data: { url: data.url || '/' },
    }),
  );
});

// Clicking a notification opens its page: in an open window of the app if there is one
// (the app navigates itself), else in a new one.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = new URL((event.notification.data && event.notification.data.url) || '/', self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
      const win = windows.find((w) => w.url.startsWith(self.location.origin));
      if (win) {
        win.postMessage({ type: 'knowpod:open', url });
        return win.focus();
      }
      return self.clients.openWindow(url);
    }),
  );
});

// The browser renewed the subscription (e.g. its keys expired): tell the server about the
// new one, with the key the old one was made with.
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil(
    (async () => {
      const res = await fetch('/api/v1/me/notifications', { credentials: 'same-origin' });
      if (!res.ok) return;
      const { publicKey } = await res.json();
      if (!publicKey) return;
      const raw = atob(publicKey.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (publicKey.length % 4)) % 4));
      const key = Uint8Array.from(raw, (c) => c.charCodeAt(0));
      const sub = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
      await fetch('/api/v1/me/notifications/subscriptions', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sub.toJSON()),
      });
    })(),
  );
});
