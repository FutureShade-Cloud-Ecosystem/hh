// Playwright drives the page in a real browser against the stubbed harness:
// the stub serves the public quote routes' contract shapes and the product
// image read, and Vite serves the page with /api proxied to the stub. Both
// servers start here, before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install, and it is the same browser the design
// system's browser leg expects to find on the runner.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));

// Ports: hashed per worktree, and stable across every process of one run,
// the same scheme the Counter Till's harness uses: the slot is a hash of this
// app's own absolute path inside a bounded band, so a different tree hashes
// elsewhere and the same tree always lands here. The guarantee that holds is
// no reuse, not distinct ports: a collision refuses loudly (the stub exits
// naming the address, vite runs with strictPort, Playwright refuses an
// occupied url). The environment overrides stay for a hand run against
// chosen ports.
function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

const slot = portSlot(appDir) * 2;
const appPort = process.env.PD_APP_PORT ?? String(24000 + slot);
const stubPort = process.env.PD_STUB_PORT ?? String(24000 + slot + 1);

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The stub holds the quote world in memory (an accepted quote flips its own
  // state), so the suite runs its tests in order, one worker, and each test
  // reads only what it itself caused or names deliberately.
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
    // The reference shots' viewport: the page is held to the reference's
    // structure at the width it was shot at.
    viewport: { width: 1440, height: 900 },
  },
  webServer: [
    {
      command: "node test/stub/public-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { PD_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { PD_STUB_PORT: stubPort, PD_APP_PORT: appPort },
    },
  ],
});
