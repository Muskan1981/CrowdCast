import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the app runs on :5173 and proxies the API and WebSocket to the
// backend (through nginx on :8080, or a single node). Production builds use the
// same origin, served by nginx, so no proxy is needed there.
const target = process.env.VITE_PROXY_TARGET || 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target, changeOrigin: true },
      '/ws': { target, changeOrigin: true, ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
  },
});
