// Playwright drives the app in a real browser against the stubbed harness:
// the demo stub serves the demo routes' shapes (the contract's DemoFlowList
// and DemoSessionResponse) and Vite serves the app with /api proxied to the
// stub. Both servers start here, before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install, and it is the same browser the design
// system's browser leg expects to find on the runner.
import { defineConfig } from "@playwright/test";

const appPort = process.env.CT_APP_PORT ?? "4820";
const stubPort = process.env.CT_STUB_PORT ?? "4821";

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/demo-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: true,
      cwd: __dirname,
      env: { CT_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: true,
      cwd: __dirname,
      env: { CT_STUB_PORT: stubPort, CT_APP_PORT: appPort },
    },
  ],
});
