// The Money Desk's acceptance leg that crosses apps: the front door's
// Payables and Receivables card opens the Money Desk on the receivables
// landing. It needs both apps on one origin (the handoff rides session
// storage), so it has its own harness (test/acceptance/serve-both.mjs) and its
// own config; the app's unit and browser legs never build the front door.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";

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
const appPort = process.env.MD_ACC_PORT ?? String(26000 + slot);
const stubPort = process.env.MD_ACC_STUB ?? String(26000 + slot + 1);

export default defineConfig({
  testDir: "./acceptance",
  testMatch: "*.pw.ts",
  timeout: 60_000,
  workers: 1,
  retries: 1,
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
      command: "node test/acceptance/serve-both.mjs",
      url: `http://127.0.0.1:${appPort}/healthz`,
      reuseExistingServer: false,
      timeout: 120_000,
      cwd: appDir,
      env: { MD_ACC_PORT: appPort, MD_ACC_STUB: stubPort, MD_ACC_OUT: join(tmpdir(), `money-desk-card-lands-${appPort}`) },
    },
  ],
});
