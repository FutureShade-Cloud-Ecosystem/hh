import { defineConfig } from 'vite';

// No COOP or COEP headers on purpose: the probe must work non-isolated.
export default defineConfig({
  optimizeDeps: { exclude: ['wa-sqlite'] },
  worker: { format: 'es' },
  server: { host: '127.0.0.1' },
});
