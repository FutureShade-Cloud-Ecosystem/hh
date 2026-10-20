// Vite configuration for the role app template.
//
// The seam rule: the front door's handoff reader is compiled in only on the
// demo build. Every other mode, dev included, resolves the empty stub
// (src/demo-account.default.ts) in place of the app's demo-account module,
// so the key name and the reader cannot enter a default build's module graph
// whatever the tree shaker keeps; app-root.ts's demo mode gate holds the
// adopt call out the same way.
//
// The dev server proxies /api to a local stub so the app's calls answer
// without a running core. The stub port comes from YARD_STUB_PORT so the
// Playwright harness and a hand-run dev server can point at the same stub.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

const stubPort = process.env.YARD_STUB_PORT ?? "4959";
const appPort = process.env.YARD_APP_PORT ?? "4958";

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
    // YARD_NO_WATCH: the harness runs the dev server with the file watcher off, so a Playwright leg survives a box
    // whose inotify instances other lanes' watchers have exhausted (EMFILE on boot).
    watch: process.env.YARD_NO_WATCH ? null : undefined,
    // The offline store's sync statechart (statecharts/sync/machine.ts) sits
    // beside the web workspace, not inside it; the data layer imports it, so
    // the dev server serves that one folder of the repository beside the
    // workspace itself (an explicit allow list replaces the default, so the
    // workspace root is named first).
    fs: {
      allow: [
        fileURLToPath(new URL("../..", import.meta.url)),
        fileURLToPath(new URL("../../../statecharts", import.meta.url)),
      ],
    },
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      "/_stub": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      // The console legs (test/console.config.ts) serve the runtime configuration from the front door's auth
      // stub; any other run has none here and the app stays in demo mode.
      "/config.json": {
        target: `http://127.0.0.1:${process.env.CW_AUTH_STUB ?? stubPort}`,
      },
    },
  },
  // wa-sqlite must not ride the prebundler (its wasm is fetched beside the
  // module, the offline-store probe's own finding), and workers stay ES
  // modules, the shape the OPFS backend's worker entry is written in.
  optimizeDeps: { exclude: ["wa-sqlite"] },
  worker: { format: "es" },
  build: {
    outDir: "dist",
  },
}));
