// Playwright drives the app in a real browser against the stubbed harness:
// the demo stub serves the demo routes' shapes (the contract's DemoFlowList
// and DemoSessionResponse) and Vite serves the app with /api proxied to the
// stub. Both servers start here, before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install, and it is the same browser the design
// system's browser leg expects to find on the runner.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));
// The ports sit away from the cycle's other lane (W8 runs beside this one on
// its own worktree and its own pair): the env carries the real values when a
// run needs a third pair.
const appPort = process.env.CT_APP_PORT ?? "4826";
const stubPort = process.env.CT_STUB_PORT ?? "4827";

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The stub holds the demo world in memory (an open drawer, committed
  // sales, parked tickets), so the suite runs its tests in order, one
  // worker, and each test reads only what it itself caused.
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
      // Reuse is a workstation convenience only: a stale server on the pair
      // must never be driven silently, least of all by CI, so under CI a
      // busy port fails the run instead of joining it.
      reuseExistingServer: !process.env.CI,
      cwd: appDir,
      env: { CT_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: !process.env.CI,
      cwd: appDir,
      env: { CT_STUB_PORT: stubPort, CT_APP_PORT: appPort },
    },
  ],
});
