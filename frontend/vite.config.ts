import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In dev the /api path is proxied to the Go backend so URLs match the production shape,
// where the backend serves both the embedded SPA and the API from one origin.
export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': { target: process.env.VITE_API_TARGET || 'http://localhost:8080', changeOrigin: true },
      '/healthz': { target: process.env.VITE_API_TARGET || 'http://localhost:8080', changeOrigin: true },
    },
  },
});
