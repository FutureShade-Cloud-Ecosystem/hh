// Playwright drives the app in a real browser against the stubbed harness.
// The stub serves the demo routes and this app's placeholder routes; Vite
// serves the app with /api proxied to the stub. Both servers start here
// before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install.
//
// The port slot is hashed per worktree so the same tree always lands on the
// same ports and different trees hash elsewhere, letting the web servers and
// the tests agree on one port without collision.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));

function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

const slot = portSlot(appDir);
const appPort = process.env.DISPATCH_APP_PORT ?? String(20000 + slot);
const stubPort = process.env.DISPATCH_STUB_PORT ?? String(20000 + slot + 1);

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The image acceptance legs run against a booted image with their own config.
  testIgnore: "acceptance/**",
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/dispatch-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DISPATCH_STUB_PORT: stubPort },
    },
    {
      // The demo mode flag is the seam rule: the front door's handoff exists
      // only where the demo is compiled in, so this harness serves the app
      // the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DISPATCH_STUB_PORT: stubPort, DISPATCH_APP_PORT: appPort, DISPATCH_NO_WATCH: "1" },
    },
  ],
});
