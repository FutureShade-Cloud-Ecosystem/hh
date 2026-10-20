// Vite configuration for Pro Pay.
//
// The dev server proxies /api and /_stub to the shared tenant stub
// (web/packages/pro-kit/test-stub/tenant-stub.mjs) so the app's calls answer
// without a running core. The stub port comes from ROLE_STUB_PORT so the
// Playwright harness and a hand-run dev server point at the same stub. The
// defaults are this app's own pair in the Pro cycle's range.
//
// The dev server also serves the sandbox processor stand-in (sandbox/hosted-field.html)
// at /processor-sandbox/hosted-field.html. It is a serve-only plugin: nothing in a build
// carries a card form.
import { defineConfig, type Plugin } from "vite";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const stubPort = process.env.ROLE_STUB_PORT ?? "56820";
const appPort = process.env.ROLE_APP_PORT ?? "56821";

const sandboxPage = fileURLToPath(new URL("./sandbox/hosted-field.html", import.meta.url));

function processorSandbox(): Plugin {
  return {
    name: "pro-pay-processor-sandbox",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/processor-sandbox/hosted-field.html", (_req, res) => {
        res.setHeader("content-type", "text/html; charset=utf-8");
        res.end(readFileSync(sandboxPage, "utf8"));
      });
    },
  };
}

export default defineConfig({
  plugins: [processorSandbox()],
  server: {
    port: Number(appPort),
    strictPort: true,
    host: "127.0.0.1",
    // ROLE_NO_WATCH: the harness runs the dev server with the file watcher off, so a Playwright leg survives a box
    // whose inotify instances other lanes' watchers have exhausted (EMFILE on boot).
    watch: process.env.ROLE_NO_WATCH ? null : undefined,
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      "/_stub": {
        target: `http://127.0.0.1:${stubPort}`,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
