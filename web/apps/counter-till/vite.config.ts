// The Counter Till's Vite configuration: one dev server and one build output
// for this one micro-app (web/apps/README.md: one Vite entry per micro-app).
//
// The dev server proxies /api to a local demo stub so the app's calls to the
// demo routes answer without a running core: the stub (test/stub/demo-stub.mjs)
// serves the flows table and the session mint in the contract's shapes
// (core/api/openapi.yaml, DemoFlowList and DemoSessionResponse). In the served
// product no proxy exists: the app and the core share one origin and every
// call is same-origin relative, which is why the app's ApiClient is built with
// an empty base URL. The proxy port comes from CT_STUB_PORT so the Playwright
// harness and a hand-run dev server can point at the same stub.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

const stubPort = process.env.CT_STUB_PORT ?? "4827";
const appPort = process.env.CT_APP_PORT ?? "4826";

export default defineConfig(({ mode }) => ({
  // The seam rule (decision log rows 893 and 899): the front door's handoff
  // reader is compiled in only on the demo build. Every other mode, dev
  // included, resolves the empty stub (src/demo-account.default.ts) in place
  // of the app's demo-account module, so the key name and the reader cannot
  // enter a default build's module graph whatever the tree shaker keeps;
  // app-root.ts's demo mode gate holds the adopt call out the same way. The
  // demo image and the Playwright harness both ask for the demo mode.
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
    // The offline store's sync statechart (statecharts/sync/machine.ts) sits
    // beside the web workspace, not inside it; the offline layer imports it,
    // so the dev server serves that one folder of the repository beside the
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
      // The stub's own peek route (test scaffolding, no contract path shares
      // the namespace): proxied so the suite reads the stub's ledgers through
      // the same origin the app uses.
      "/_stub": {
        target: `http://127.0.0.1:${stubPort}`,
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
