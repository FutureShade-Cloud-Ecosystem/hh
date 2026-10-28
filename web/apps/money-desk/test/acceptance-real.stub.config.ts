// The stub variant of the real config (W2-4-HARNESS-INTERFACE-r1): the same specs, run against
// the stub, where they must be RED, because the stub's world is not the core's world.
//
// The real config (test/acceptance-real.config.ts, the part (a) brief lands) serves the same
// specs against the core box under test. This one serves them against the stub: the stub answers
// the core's routes, the app is served the way the demo image builds it (vite --mode demo, so
// the front door's handoff is compiled in), and the specs' direct reads of the core's API go to
// the stub through coreURL().
//
// The ports are hashed per tree and sit in their own band, so this config never collides with the
// app's default config (20000 and up) or with the acceptance config (26000 and up).
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));

function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 1024;
}

const slot = portSlot(`${appDir}:acceptance-real-stub`) % 512;
const appPort = process.env.MD_REAL_STUB_APP_PORT ?? String(24000 + slot);
const stubPort = process.env.MD_REAL_STUB_PORT ?? String(24000 + slot + 1);

// The specs read the core's API directly through coreURL() (./session), so the stub's base URL is
// named here for the test process. The workers inherit it from this process.
process.env.HH_CORE_URL = `http://127.0.0.1:${stubPort}`;

export default defineConfig({
  testDir: "./acceptance-real",
  testMatch: "*.pw.ts",
  timeout: 120_000,
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/money-desk-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort },
    },
    {
      // The demo mode flag is the seam rule: the front door's handoff exists only where the demo is
      // compiled in, so this harness serves the app the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      timeout: 120_000,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort, ROLE_APP_PORT: appPort },
    },
  ],
});
