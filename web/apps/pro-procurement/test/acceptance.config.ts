// The acceptance legs' own harness: the app and the stub on this app's
// acceptance pair on free ports taken at config load (overridable inline with
// PRO_ACC_STUB and PRO_ACC_PORT), so the unit and surface legs' pair never overlaps it. The same shape as
// Buying and Stock's acceptance.config.ts.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { freePorts } from "@hardscapehouse/pro-kit/test-stub/free-port.mjs";

const appDir = fileURLToPath(new URL("..", import.meta.url));
const [freeApp, freeStub] = await freePorts(2);
process.env.PRO_ACC_PORT ??= freeApp;
process.env.PRO_ACC_STUB ??= freeStub;
const appPort = process.env.PRO_ACC_PORT;
const stubPort = process.env.PRO_ACC_STUB;

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
