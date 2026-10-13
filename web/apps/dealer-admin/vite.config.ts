// Vite configuration for Dealer Admin.
//
// The seam rule: the front door's handoff reader is compiled in only on the
// demo build. Every other mode, dev included, resolves the empty stub
// (src/demo-account.default.ts) in place of the app's demo-account module,
// so the key name and the reader cannot enter a default build's module graph
// whatever the tree shaker keeps; app-root.ts's demo mode gate holds the
// adopt call out the same way.
//
// The dev server proxies /api to a local stub so the app's calls answer
// without a running core. The stub port comes from DA_STUB_PORT so the
// Playwright harness and a hand-run dev server can point at the same stub.
// The pair sits in the mod-c lane's own port range (56600 to 56699):
// stub 56610, app 56611.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

const stubPort = process.env.DA_STUB_PORT ?? "56610";
const appPort = process.env.DA_APP_PORT ?? "56611";

export default defineConfig(({ mode }) => ({
  resolve:
    mode === "demo"
      ? {}
      : {
          alias: [
            {
              find: /^\.\/demo-account\.js$/,
              replacement: fileURLToPath(new URL("./src/demo-account.default.ts", import.meta.url)),
            },
          ],
        },
  server: {
    port: Number(appPort),
    strictPort: true,
    host: "127.0.0.1",
    // DA_NO_WATCH: the harness runs the dev server with the file watcher
    // off, so a Playwright leg survives a box whose inotify instances other
    // lanes' watchers have exhausted (EMFILE on boot).
    watch: process.env.DA_NO_WATCH ? null : undefined,
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
}));
