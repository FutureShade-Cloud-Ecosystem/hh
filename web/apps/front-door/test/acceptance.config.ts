// The front door's acceptance configuration: the D2 brief's seven behaviours,
// driven in a real browser against the stubbed harness (the stub serves the
// mint in the contract's shape; Vite serves the app with /api proxied to the
// stub). This is the app's own test suite, so `npm test` runs it; the demo
// image itself is D1's assembly, and the box-level walk belongs to the
// image's own exit test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install, the same posture as the Counter Till's
// harness.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));

// Ports: the same hashed-slot scheme the Counter Till's harness uses, keyed
// on this app's own absolute path, so the two apps' servers never fight over
// one port on one machine. The environment overrides stay for a hand run.
function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

const slot = portSlot(appDir) * 2;
const appPort = process.env.FD_APP_PORT ?? String(24000 + slot);
const stubPort = process.env.FD_STUB_PORT ?? String(24000 + slot + 1);

export default defineConfig({
  testDir: ".",
  testMatch: "acceptance/*.pw.ts",
  timeout: 30_000,
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
      reuseExistingServer: false,
      cwd: appDir,
      env: { FD_STUB_PORT: stubPort },
    },
    {
      command: `npx --no-install vite --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { FD_STUB_PORT: stubPort, FD_APP_PORT: appPort },
    },
  ],
});
