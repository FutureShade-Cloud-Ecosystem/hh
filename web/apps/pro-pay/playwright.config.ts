// Playwright drives Pro Pay in a real browser against the shared tenant stub.
// The stub serves the tenant routes; Vite serves the app with /api and /_stub
// proxied to it. Both servers start here before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install. The stub and app ports are free ones taken at
// config load (never a fixed pair, which an overlapping suite's client sockets can
// hold), overridable inline with ROLE_STUB_PORT and ROLE_APP_PORT.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { freePorts } from "@hardscapehouse/pro-kit/test-stub/free-port.mjs";

const appDir = fileURLToPath(new URL(".", import.meta.url));
const [freeApp, freeStub] = await freePorts(2);
process.env.ROLE_APP_PORT ??= freeApp;
process.env.ROLE_STUB_PORT ??= freeStub;
const appPort = process.env.ROLE_APP_PORT;
const stubPort = process.env.ROLE_STUB_PORT;

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
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
