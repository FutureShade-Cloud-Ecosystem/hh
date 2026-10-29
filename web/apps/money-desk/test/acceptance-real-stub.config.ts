// The acceptance-real specs against the Node stub (W2-4 (a)): the same specs,
// the stub's world.
//
// WHY A SECOND CONFIG. The acceptance-real legs live in test/acceptance-real
// and the app's default config ignores that directory, so the stub has never
// had a way to run them. This config is that way: the same specs, the stub's
// world.
//
// WHAT IS RED HERE, AND WHY IT IS ONLY THAT. The stub's world is not the
// core's, so the pinned figure legs are red under the stub by design: a spec
// must fail on the figure, never on a missing session. That is exactly what
// this config proves. asStaff signs the session in the stub's own shape (the
// front door's demo mint, the way the harness's stub leg signed in), so the
// way in is the same in both worlds.
//
// The ports are the app's default config's rule with this tree's own slot, so
// a stub run never lands on the real box's ports.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));
// The hash input is the directory without a trailing slash, the same string
// the real config hashes.
const slotInput = appDir.replace(/[/\\]+$/, "");

function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 512;
}

const slot = portSlot(slotInput);
const appPort = process.env.MD_STUB_APP_PORT ?? String(24000 + slot * 2);
const stubPort = process.env.MD_STUB_PORT ?? String(24000 + slot * 2 + 1);

// This config is the stub, never the box: no box state, so session.ts signs
// in the way the stub's own mint signs in, and no MDREAL_CONFIG marker, so
// the specs know which world they are in.
delete process.env.MDREAL_STATE;
delete process.env.MDREAL_CONFIG;
// The stub's base URL, the answer coreURL() gives under the stub.
process.env.MD_STUB_URL = `http://127.0.0.1:${stubPort}`;

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
      timeout: 60_000,
      cwd: appDir,
      env: { ROLE_STUB_PORT: String(stubPort) },
    },
    {
      // The demo mode flag is the seam rule: the front door's handoff exists
      // only where the demo is compiled in, so this harness serves the app
      // the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      timeout: 180_000,
      cwd: appDir,
      env: { ROLE_STUB_PORT: String(stubPort), ROLE_APP_PORT: String(appPort) },
    },
  ],
});
