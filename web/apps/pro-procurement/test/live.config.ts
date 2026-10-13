// The live legs' own harness: the front door and the three Pro apps on one origin (test/live/serve-all.mjs), the
// shared tenant stub behind them. The pair is in the Pro cycle's range (stub 56860, app 56861); the other
// harnesses' pairs stay free.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";

const appDir = fileURLToPath(new URL("..", import.meta.url));
const appPort = process.env.PRO_LIVE_PORT ?? "56861";
const stubPort = process.env.PRO_LIVE_STUB ?? "56860";

export default defineConfig({
  testDir: "./live",
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
      command: "node ../../packages/pro-kit/test-stub/tenant-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { ROLE_STUB_PORT: stubPort },
    },
    {
      command: "node test/live/serve-all.mjs",
      url: `http://127.0.0.1:${appPort}/healthz`,
      reuseExistingServer: false,
      timeout: 240_000,
      cwd: appDir,
      env: { PRO_LIVE_PORT: appPort, PRO_LIVE_STUB: stubPort, PRO_LIVE_OUT: join(tmpdir(), `pro-live-${appPort}`) },
    },
  ],
});
