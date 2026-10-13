// The Quote Desk's acceptance story with the desk served by this config itself: a Vite dev server in demo mode at the
// root of QD_ACCEPT_URL, with /api proxied to the demo box (vite.config.ts reads the proxy target from QD_STUB_PORT, so
// the box's host port goes there). Playwright starts the server before the story and ends it with the run, so nothing
// stays behind. It is the acceptance config of this directory plus the web server, nothing else.
//
//   QD_APP_PORT       the desk's port (must match the port in QD_ACCEPT_URL)
//   QD_STUB_PORT      the demo box's host port (the proxy target)
//   QD_ACCEPT_URL     http://127.0.0.1:<QD_APP_PORT>/
//   QD_ACCEPT_USER, QD_ACCEPT_PHRASE  as in acceptance.config.ts
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";
import base from "./acceptance.config";

const appDir = fileURLToPath(new URL("../..", import.meta.url));
const appPort = process.env.QD_APP_PORT ?? "";
const boxPort = process.env.QD_STUB_PORT ?? "";

export default defineConfig({
  ...base,
  webServer: {
    command: `npx --no-install vite --mode demo --port ${appPort} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${appPort}/`,
    reuseExistingServer: false,
    cwd: appDir,
    env: { QD_APP_PORT: appPort, QD_STUB_PORT: boxPort },
  },
});
