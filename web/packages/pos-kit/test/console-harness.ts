// The Playwright configuration for an app's console legs (item 7d). One call builds the five servers a leg
// needs: the front door's stub (the fake FB Console and the core's auth routes), the app's own stub (its data
// routes), the router that puts both behind one credentialed cross origin core, the front door's dev server
// and the app's dev server (default mode, so no demo handoff exists in the page). The pages reach the router by
// its absolute address, the way the deployed pages reach the core on another host; the cookie is shared across
// the local ports because cookies ignore ports.
//
// Ports come from the range given (five consecutive), free ports checked by the caller; the environment
// overrides stay for a hand run.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

export interface ConsoleHarnessOptions {
  /** The app's directory (its own stub and dev server run from here). */
  appDir: string;
  /** The app's stub command, relative to appDir, and the environment variable that names its port. */
  appStub: { command: string; portEnv: string; health?: string };
  /** The app's dev server port variables (the app's vite.config reads the stub port from one of them). */
  appVite: { stubPortEnv: string; appPortEnv: string };
  /** The first of five consecutive ports. */
  firstPort: number;
}

const FRONT_DOOR_DIR = fileURLToPath(new URL("../../../apps/front-door/", import.meta.url));
const WORLD = fileURLToPath(new URL("./console-world.mjs", import.meta.url));

export function consoleHarness(options: ConsoleHarnessOptions) {
  const base = Number(process.env.CW_FIRST_PORT ?? options.firstPort);
  const ports = { authStub: base, appStub: base + 1, world: base + 2, frontDoor: base + 3, app: base + 4 };
  // The legs read these to build the served configuration and to reach each server.
  Object.assign(process.env, {
    CW_AUTH_STUB: String(ports.authStub),
    CW_WORLD: String(ports.world),
    CW_FRONT_DOOR: String(ports.frontDoor),
    CW_APP: String(ports.app),
  });
  return defineConfig({
    testDir: ".",
    testMatch: "console.pw.ts",
    timeout: 45_000,
    workers: 1,
    use: { channel: "chrome", headless: true },
    webServer: [
      {
        command: "node test/stub/demo-stub.mjs",
        url: `http://127.0.0.1:${ports.authStub}/healthz`,
        reuseExistingServer: false,
        cwd: FRONT_DOOR_DIR,
        env: { FD_STUB_PORT: String(ports.authStub) },
      },
      {
        command: options.appStub.command,
        url: `http://127.0.0.1:${ports.appStub}${options.appStub.health ?? "/healthz"}`,
        reuseExistingServer: false,
        cwd: options.appDir,
        env: { [options.appStub.portEnv]: String(ports.appStub) },
      },
      {
        command: `node ${WORLD}`,
        url: `http://127.0.0.1:${ports.world}/healthz`,
        reuseExistingServer: false,
        env: { CW_PORT: String(ports.world), CW_AUTH_PORT: String(ports.authStub), CW_APP_PORT: String(ports.appStub) },
      },
      {
        command: `npx --no-install vite --port ${ports.frontDoor} --strictPort --host 127.0.0.1`,
        url: `http://127.0.0.1:${ports.frontDoor}/`,
        reuseExistingServer: false,
        cwd: FRONT_DOOR_DIR,
        env: { FD_STUB_PORT: String(ports.authStub), FD_APP_PORT: String(ports.frontDoor), CHOKIDAR_USEPOLLING: "1" },
      },
      {
        command: `npx --no-install vite --port ${ports.app} --strictPort --host 127.0.0.1`,
        url: `http://127.0.0.1:${ports.app}/`,
        reuseExistingServer: false,
        cwd: options.appDir,
        env: {
          [options.appVite.stubPortEnv]: String(ports.appStub),
          [options.appVite.appPortEnv]: String(ports.app),
          // The served runtime configuration comes from the auth stub through the page's own origin.
          CW_AUTH_STUB: String(ports.authStub),
          CHOKIDAR_USEPOLLING: "1",
        },
      },
    ],
  });
}
