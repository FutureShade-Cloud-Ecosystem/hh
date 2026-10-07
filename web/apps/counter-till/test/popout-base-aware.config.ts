// Playwright config for the base-aware popout leg: the same till app and
// stub, but built and served with --base=/till/ (the demo box's deployment
// shape). The leg runs against the vite dev server started with that base,
// so the app's import.meta.env.BASE_URL is "/till/" at runtime. On main
// (the bug) the pop-out opens /?popout=<flow> (the front door picker).
// After the fix it opens /till/?popout=<flow> (the till's own sale surface).
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

const slot = portSlot(appDir) * 2 + 99; // +99 to avoid colliding with the default config's ports
const appPort = process.env.CT_APP_PORT ?? String(20000 + slot);
const stubPort = process.env.CT_STUB_PORT ?? String(20000 + slot + 1);

export default defineConfig({
  testDir: ".",
  testMatch: ["popout-base-aware.pw.ts", "handoff-prefixed.pw.ts"],
  use: {
    baseURL: `http://127.0.0.1:${appPort}/till/`,
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
      // --base=/till/ makes Vite serve the app under that path, so
      // import.meta.env.BASE_URL is "/till/" at runtime and the pop-out
      // lands on the till's own sale surface, not the front door picker.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1 --base /till/`,
      url: `http://127.0.0.1:${appPort}/till/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { CT_STUB_PORT: stubPort, CT_APP_PORT: appPort },
    },
  ],
});
