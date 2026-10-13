// The acceptance legs' own harness: the app and the stub on this app's
// acceptance pair in the Pro cycle's range (stub 56850, app 56851), so the
// unit and surface legs' pair (56810, 56811) stays free. The same shape as
// Buying and Stock's acceptance.config.ts.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));
const appPort = process.env.PRO_ACC_PORT ?? "56851";
const stubPort = process.env.PRO_ACC_STUB ?? "56850";

export default defineConfig({
  testDir: "./acceptance",
  testMatch: "*.pw.ts",
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node ../../packages/pro-kit/test-stub/tenant-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort, ROLE_APP_PORT: appPort },
    },
  ],
});
