// Playwright drives the front door in a real browser against the stubbed harness:
// the stub serves the public dealer config and the demo handoff shapes, and Vite
// serves the app with /api proxied to the stub. Both servers start here, before
// any test. The acceptance suite has its own config (test/acceptance.config.ts).
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { freePorts } from "./test/free-ports.js";

const appDir = fileURLToPath(new URL(".", import.meta.url));

// Ports: two free ones, taken from the system at config load (RULES-S4
// addendum 7: a test server never pins a port, because a pinned one collides
// by chance with whatever the system hands out at that moment). Playwright
// evaluates this config twice, once in the process that starts the web servers
// and once in the worker that runs the tests, so the environment carries the
// pair from the first evaluation to the second. The overrides stay for a hand
// run against chosen ports.
const [freeApp, freeStub] = await freePorts(2);
process.env.FD_APP_PORT ??= freeApp;
process.env.FD_STUB_PORT ??= freeStub;
const appPort = String(process.env.FD_APP_PORT);
const stubPort = String(process.env.FD_STUB_PORT);

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  testIgnore: ["**/acceptance/**"],
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/demo-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { FD_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { FD_STUB_PORT: stubPort, FD_APP_PORT: appPort },
    },
  ],
});
