// Playwright drives Dealer Admin in a real browser against the stubbed
// harness. The stub serves the demo routes and this app's routes; Vite
// serves the app with /api proxied to the stub. Both servers start here
// before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install.
//
// The ports are the app's own pair in the mod-c lane's range (stub 56610,
// app 56611), overridable by DA_APP_PORT and DA_STUB_PORT so a second tree
// or the demo round can move the run without an edit.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));
const appPort = process.env.DA_APP_PORT ?? "56611";
const stubPort = process.env.DA_STUB_PORT ?? "56610";

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
      command: "node test/stub/dealer-admin-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DA_STUB_PORT: stubPort },
    },
    {
      // The demo mode flag is the seam rule: the front door's handoff exists
      // only where the demo is compiled in, so this harness serves the app
      // the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DA_STUB_PORT: stubPort, DA_APP_PORT: appPort, DA_NO_WATCH: "1" },
    },
  ],
});
