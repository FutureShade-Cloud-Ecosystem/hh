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

// Ports: hashed per worktree, and stable across every process of one run. The
// slot is a hash of this app's own absolute path inside a bounded band below
// the ephemeral range: a different tree (W7 and W8 run beside this lane on
// the same machine) hashes elsewhere, the same tree always lands here, and
// the same value in every process is what lets the web servers and the tests
// agree on one port (a per process derivation, the process id, does not: the
// worker re-imports this config). The guarantee that holds is no reuse, not
// distinct ports: different trees CAN land on the same slot, so the servers
// are never reused and a collision refuses loudly rather than testing a
// stranger: the stub exits naming the address, vite runs with strictPort, and
// Playwright refuses an occupied url. The environment overrides stay for a
// hand run against chosen ports.
function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

const slot = portSlot(appDir) * 2;
const appPort = process.env.CT_APP_PORT ?? String(20000 + slot);
const stubPort = process.env.CT_STUB_PORT ?? String(20000 + slot + 1);

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
      reuseExistingServer: false,
      cwd: appDir,
      env: { CT_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { CT_STUB_PORT: stubPort, CT_APP_PORT: appPort },
    },
  ],
});
