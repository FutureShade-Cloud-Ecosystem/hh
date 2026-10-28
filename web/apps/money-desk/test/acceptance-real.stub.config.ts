// The stub variant of the real harness's config: the same specs, run against
// the stub instead of a real core box. The interface allows a small stub
// variant of the real config, and this is it: the specs live in
// test/acceptance-real and must FAIL here, because the stub's world is not the
// core's. The red is the proof that a spec reads the core's world and not the
// stub's.
//
// This config is not part of the package's test script, so the web shard does
// not run it: a leg that is red by design has no place in a green shard.
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

const slot = portSlot(appDir) * 3;
const appPort = process.env.MD_REAL_APP_PORT ?? String(28000 + slot);
const stubPort = process.env.MD_REAL_STUB ?? String(28000 + slot + 1);

// The box's base URL for a spec that reads the core's API directly: the
// Money Desk's own origin, whose /api goes to the server under test.
process.env.W2_4B_CORE_URL ??= `http://127.0.0.1:${appPort}`;

export default defineConfig({
  testDir: "./acceptance-real",
  testMatch: "*.pw.ts",
  timeout: 60_000,
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
      // The demo mode flag is the seam rule: the front door's handoff exists
      // only where the demo is compiled in, so this harness serves the app
      // the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort, ROLE_APP_PORT: appPort },
    },
  ],
});
