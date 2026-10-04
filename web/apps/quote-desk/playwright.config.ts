// Playwright drives the app in a real browser against the stubbed harness:
// the quote stub serves the demo routes' and the quote operations' contract
// shapes and Vite serves the app with /api proxied to the stub. Both servers
// start here, before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install, and it is the same browser the design
// system's browser leg expects to find on the runner.
//
// The port slot is hashed per worktree on the counter-till's own pattern: a
// different tree hashes elsewhere, the same tree always lands here, and the
// same value in every process is what lets the web servers and the tests
// agree on one port. The guarantee that holds is no reuse, not distinct
// ports: the servers are never reused and a collision refuses loudly (the
// stub exits naming the address, vite runs with strictPort, and Playwright
// refuses an occupied url).
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

const slot = portSlot(appDir) * 2;
const appPort = process.env.QD_APP_PORT ?? String(20000 + slot);
const stubPort = process.env.QD_STUB_PORT ?? String(20000 + slot + 1);

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The stub holds the quote world in memory (draft quotes, their lines, the
  // workspace rows), so the suite runs its tests in order, one worker, and
  // each test reads only what it itself caused.
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/qd-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { QD_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { QD_STUB_PORT: stubPort, QD_APP_PORT: appPort },
    },
  ],
});
