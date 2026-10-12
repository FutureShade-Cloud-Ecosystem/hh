import { defineConfig } from '@playwright/test';

const port = Number(process.env.PROBE_PORT);
if (!Number.isInteger(port) || port < 56200 || port > 56299) {
  throw new Error('set PROBE_PORT to a free port in 56200 to 56299, inline on the command');
}

export default defineConfig({
  testDir: './test',
  testMatch: '**/*.pw.js',
  workers: 1,
  reporter: 'list',
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: `npx vite --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: false,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
