// Playwright drives the front door in a real browser against the stubbed harness:
// the stub serves the public dealer config and the demo handoff shapes, and Vite
// serves the app with /api proxied to the stub. Both servers start here, before
// any test. The acceptance suite has its own config (test/acceptance.config.ts).
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL(".", import.meta.url));

// Ports: hashed per worktree, stable across every process of one run, the same
// scheme the Counter Till's harness uses. The environment overrides stay for a
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
const appPort = process.env.FD_APP_PORT ?? String(24000 + slot);
const stubPort = process.env.FD_STUB_PORT ?? String(24000 + slot + 1);

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  testIgnore: ["**/acceptance/**"],
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
