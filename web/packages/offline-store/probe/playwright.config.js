import { defineConfig } from '@playwright/test';

const port = Number(process.env.PROBE_PORT);

export default defineConfig({
  testDir: './test',
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
