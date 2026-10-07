// Playwright config for the prefixed handoff leg: the same desk and stub, served under /quote/ (the demo box's
// shape). Under a prefix the front door is at the origin root, so a person with no session is sent there to pick
// a role (decision log row 1040); at the origin root, where the main config serves the desk, the desk's own entry
// cards are the way in. The ports sit beside the main config's, never on them.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));

function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

const slot = portSlot(appDir) * 2 + 99;
const appPort = process.env.QD_PREFIXED_APP_PORT ?? String(20000 + slot);
const stubPort = process.env.QD_PREFIXED_STUB_PORT ?? String(20000 + slot + 1);

export default defineConfig({
  testDir: ".",
  testMatch: ["front-door-prefixed.spec.ts"],
  use: {
    baseURL: `http://127.0.0.1:${appPort}/quote/`,
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
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1 --base /quote/`,
      url: `http://127.0.0.1:${appPort}/quote/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { QD_STUB_PORT: stubPort, QD_APP_PORT: appPort },
    },
  ],
});
