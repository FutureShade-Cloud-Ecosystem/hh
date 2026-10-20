// The shell chrome's browser leg: a Vite dev server over the rail harness
// page (test/pw-harness/), the system Chrome, a port given inline
// (SC_APP_PORT=<free>). No stub: the rail's pin menu needs only a page.
import { defineConfig } from "@playwright/test";
import { fileURLToPath } from "node:url";

const pkgDir = fileURLToPath(new URL(".", import.meta.url));
const port = process.env.SC_APP_PORT ?? "57170";

export default defineConfig({
  testDir: "./test",
  testMatch: "**/*.pw.ts",
  workers: 1,
  use: { baseURL: `http://127.0.0.1:${port}`, channel: "chrome", headless: true },
  webServer: {
    command: `npx --no-install vite --config ./test/pw-harness/vite.config.mjs --port ${port} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${port}/test/pw-harness/index.html`,
    reuseExistingServer: false,
    cwd: pkgDir,
  },
});
