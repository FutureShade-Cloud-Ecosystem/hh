// The live legs' own harness: the front door and the three Pro apps on one origin (test/live/serve-all.mjs), the
// shared tenant stub behind them. The stub and app ports are free ones taken at config load, overridable
// inline with PRO_LIVE_STUB and PRO_LIVE_PORT.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { freePorts } from "@hardscapehouse/pro-kit/test-stub/free-port.mjs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const appDir = fileURLToPath(new URL("..", import.meta.url));
const [freeApp, freeStub] = await freePorts(2);
process.env.PRO_LIVE_PORT ??= freeApp;
process.env.PRO_LIVE_STUB ??= freeStub;
const appPort = process.env.PRO_LIVE_PORT;
const stubPort = process.env.PRO_LIVE_STUB;

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
