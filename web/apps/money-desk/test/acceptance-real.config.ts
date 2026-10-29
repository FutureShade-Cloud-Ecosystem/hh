// The Money Desk's acceptance-real config (W2-4 (a)): the specs in
// test/acceptance-real run against the finance image on a throwaway
// database (test/acceptance-real/box.sh starts it with the run's throwaway
// session key), never against the Node stub. The desk the box serves is the
// console build; it recovers its session on load through the real exchange
// (the console double answers the platform directory), so every request the
// specs make runs the real core's authz with the run's own staff session.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";

const appDir = fileURLToPath(new URL("..", import.meta.url));
// The hash input is the directory without a trailing slash, exactly the
// string cd/pwd hands box.sh.
const slotInput = appDir.replace(/[/\\]+$/, "");

// The same FNV-1a slot box.sh computes: this tree's ports never collide
// with another worktree's box. A run may override either port by name.
function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 512;
}

const slot = portSlot(slotInput);
// The state directory carries the tree's slot too, so two worktrees beside
// each other keep two state directories and never read each other's.
const stateDir = process.env.MDREAL_STATE ?? join(tmpdir(), `money-desk-acceptance-real-${slot}`);
const boxPort = process.env.MDREAL_BOX_PORT ?? String(27000 + slot * 2);
const consolePort = process.env.MDREAL_CONSOLE_PORT ?? String(27000 + slot * 2 + 1);
const pgPort = process.env.MDREAL_PG_PORT ?? String(28000 + slot * 2);
// The tree's name for its box: the same slot the ports use, or the name the
// run gives it. The box's containers and network carry it, so one tree's box
// is never another tree's box.
const boxName = process.env.MDREAL_NAME ?? `s4-w2-4a-${slot}`;
process.env.MDREAL_STATE = stateDir;
// The marker the specs read: this config is the real box, never the stub.
process.env.MDREAL_CONFIG = "1";

const boxUrl = `http://127.0.0.1:${boxPort}`;

export default defineConfig({
  testDir: "./acceptance-real",
  testMatch: "*.pw.ts",
  timeout: 180_000,
  workers: 1,
  use: {
    baseURL: `${boxUrl}/money-desk/`,
    channel: "chrome",
    headless: true,
  },
  webServer: {
    // The box comes up and the holder keeps the webServer process alive for
    // the whole run: Playwright owns that process until the run ends, and
    // `box.sh up` alone returns the moment the health answers, which is a
    // race with Playwright's own probe of the URL.
    command: "bash test/acceptance-real/box.sh up && bash test/acceptance-real/box.sh hold",
    url: `${boxUrl}/healthz`,
    // CI always starts a fresh box; a developer may keep one up by hand
    // (`box.sh up`) and iterate against it.
    reuseExistingServer: process.env.CI !== "true",
    timeout: 600_000,
    cwd: appDir,
    env: {
      MDREAL_STATE: stateDir,
      MDREAL_BOX_PORT: boxPort,
      MDREAL_CONSOLE_PORT: consolePort,
      MDREAL_PG_PORT: pgPort,
      MDREAL_NAME: boxName,
      ...(process.env.MDREAL_IMAGE === undefined ? {} : { MDREAL_IMAGE: process.env.MDREAL_IMAGE }),
      ...(process.env.MDREAL_NODE === undefined ? {} : { MDREAL_NODE: process.env.MDREAL_NODE }),
      ...(process.env.MDREAL_SUBJECT === undefined ? {} : { MDREAL_SUBJECT: process.env.MDREAL_SUBJECT }),
    },
  },
  globalTeardown: "./acceptance-real/teardown.mjs",
});
