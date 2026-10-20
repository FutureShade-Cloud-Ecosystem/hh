// Playwright drives the app in a real browser against the stubbed harness.
// The stub serves the demo routes and this app's placeholder routes; Vite
// serves the app with /api proxied to the stub. Both servers start here
// before any test.
//
// The browser is the system Chrome through the chrome channel: no browser
// download rides the npm install.
//
// The stub and app ports are free ones taken once at config load (never a
// fixed pair or worktree hash, which an overlapping suite can hold),
// overridable inline with DISPATCH_APP_PORT and DISPATCH_STUB_PORT.
import { defineConfig } from "@playwright/test";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

// Two distinct free ports: both listeners stay open until both are known.
async function freePorts(count: number): Promise<string[]> {
  const servers = await Promise.all(
    Array.from({ length: count }, () => new Promise<ReturnType<typeof createServer>>((resolve, reject) => {
      const s = createServer();
      s.once("error", reject);
      s.listen(0, "127.0.0.1", () => resolve(s));
    })),
  );
  const ports = servers.map((s) => String((s.address() as { port: number }).port));
  await Promise.all(servers.map((s) => new Promise<void>((done) => s.close(() => done()))));
  return ports;
}

const appDir = fileURLToPath(new URL(".", import.meta.url));
const [freeApp, freeStub] = await freePorts(2);
process.env.DISPATCH_APP_PORT ??= freeApp;
process.env.DISPATCH_STUB_PORT ??= freeStub;
const appPort = process.env.DISPATCH_APP_PORT;
const stubPort = process.env.DISPATCH_STUB_PORT;

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  // The image acceptance legs run against a booted image with their own config.
  testIgnore: "acceptance/**",
  workers: 1,
  use: {
    baseURL: `http://127.0.0.1:${appPort}`,
    channel: "chrome",
    headless: true,
  },
  webServer: [
    {
      command: "node test/stub/dispatch-stub.mjs",
      url: `http://127.0.0.1:${stubPort}/healthz`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DISPATCH_STUB_PORT: stubPort },
    },
    {
      // The demo mode flag is the seam rule: the front door's handoff exists
      // only where the demo is compiled in, so this harness serves the app
      // the way the demo image builds it.
      command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
      url: `http://127.0.0.1:${appPort}/`,
      reuseExistingServer: false,
      cwd: appDir,
      env: { DISPATCH_STUB_PORT: stubPort, DISPATCH_APP_PORT: appPort, DISPATCH_NO_WATCH: "1" },
    },
  ],
});
