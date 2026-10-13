// The Work shell host's acceptance configuration. No webServer block: each
// worker starts its own server on port 0 from the fixture in
// acceptance/fixtures.ts and closes it when the worker ends (RULES-S4
// addendum 7: a test server never pins a port). The browser is the system
// Chrome through the chrome channel, like the other apps' harnesses.
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: "acceptance/*.pw.ts",
  timeout: 60_000,
  workers: 1,
  use: {
    channel: "chrome",
    headless: true,
  },
});
