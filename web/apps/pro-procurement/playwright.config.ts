// Playwright drives Pro Procurement Board in a real browser against the shared tenant stub.
// The stub serves the tenant routes; Vite serves the app with /api and /_stub
// proxied to it. Both servers start here before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install. The ports are this app's pair in the Pro
// cycle's range (stub 56810, app 56811), overridable inline.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));
const appPort = process.env.ROLE_APP_PORT ?? "56811";
const stubPort = process.env.ROLE_STUB_PORT ?? "56810";

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The acceptance legs have their own harness (test/acceptance.config.ts), the
  // Buying and Stock pattern.
  testIgnore: ["**/acceptance/**", "**/live/**"],
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
