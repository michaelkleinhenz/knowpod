import { readFileSync } from 'node:fs';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';

// The web app is installable (PWA): a manifest plus a service worker that caches the app
// shell so it starts instantly and offline. The service worker never answers API calls;
// the app itself keeps copies of the user's notes for offline reading (src/api/offline.ts).
// It also shows task reminders sent through Web Push (public/push-sw.js).
// In dev the /api path is proxied to the Go backend so URLs match production, where
// the backend serves both the embedded SPA and the API from one origin.
// The app version comes from the VERSION file at the repository root (see README.md).
const version = readFileSync(new URL('../VERSION', import.meta.url), 'utf8').trim();

export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(version),
  },
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      injectRegister: 'script-defer',
      includeAssets: ['favicon.svg', 'apple-touch-icon.png'],
      manifest: {
        name: 'knowpod',
        short_name: 'knowpod',
        description: 'Your recorded conversations, transcribed and summarized.',
        id: '/',
        start_url: '/briefing',
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        theme_color: '#1e2a3a',
        background_color: '#eef1f5',
        icons: [
          { src: '/pwa-192.png', sizes: '192x192', type: 'image/png' },
          { src: '/pwa-512.png', sizes: '512x512', type: 'image/png' },
          { src: '/pwa-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
        // Other apps can share links, text, photos, PDFs and audio with the installed app
        // (public/share-sw.js takes them, src/pages/Share.tsx saves them).
        share_target: {
          action: '/share',
          method: 'POST',
          enctype: 'multipart/form-data',
          params: {
            title: 'title',
            text: 'text',
            url: 'url',
            files: [{ name: 'files', accept: ['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'application/pdf', 'audio/wav', 'audio/x-wav', 'audio/mpeg', 'text/markdown', '.jpg', '.jpeg', '.png', '.webp', '.gif', '.pdf', '.wav', '.mp3', '.md', '.markdown'] }],
          },
        },
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,webmanifest}'],
        // Shows the task reminders sent through Web Push (public/push-sw.js) and takes what
        // other apps share with the installed app (public/share-sw.js).
        importScripts: ['push-sw.js', 'share-sw.js'],
        navigateFallback: '/index.html',
        // Never answer API calls, audio, the health check or the API description from the
        // app-shell fallback.
        navigateFallbackDenylist: [/^\/api\//, /^\/healthz/, /^\/mcp$/, /^\/\.well-known\//],
        cleanupOutdatedCaches: true,
      },
    }),
  ],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': { target: process.env.VITE_API_TARGET || 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: process.env.VITE_API_TARGET || 'http://localhost:8080', changeOrigin: true },
    },
  },
});
