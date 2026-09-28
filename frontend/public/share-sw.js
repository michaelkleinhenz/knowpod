// Imported into the generated service worker (see vite.config.ts). The installed app is a
// share target (the manifest's share_target): what other apps share with it is posted to
// /share. The service worker keeps the text and files in a cache and opens the app's share
// page, which saves them as notes (src/pages/Share.tsx).
const SHARE_CACHE = 'knowpod-share';

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (event.request.method !== 'POST' || url.origin !== self.location.origin || url.pathname !== '/share') return;
  event.respondWith(
    (async () => {
      try {
        const form = await event.request.formData();
        const cache = await caches.open(SHARE_CACHE);
        for (const key of await cache.keys()) await cache.delete(key);
        const text = (name) => {
          const v = form.get(name);
          return typeof v === 'string' ? v : '';
        };
        const meta = { title: text('title'), text: text('text'), url: text('url'), files: [], at: Date.now() };
        let i = 0;
        for (const file of form.getAll('files')) {
          if (typeof file === 'string') continue;
          const key = `/share-data/file-${i++}`;
          await cache.put(key, new Response(file, { headers: { 'Content-Type': file.type || 'application/octet-stream' } }));
          meta.files.push({ key, name: file.name, type: file.type, size: file.size, lastModified: file.lastModified });
        }
        await cache.put('/share-data/meta', new Response(JSON.stringify(meta), { headers: { 'Content-Type': 'application/json' } }));
        return Response.redirect('/share?received=1', 303);
      } catch {
        return Response.redirect('/share?missed=1', 303);
      }
    })(),
  );
});
