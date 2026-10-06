// The Quote Desk's Vite configuration: one dev server and one build output
// for this one micro-app (web/apps/README.md: one Vite entry per micro-app).
//
// The dev server proxies /api to a local quote stub so the app's calls to the
// quote routes answer without a running core: the stub
// (test/stub/qd-stub.mjs) serves the demo way in and the quote operations in
// the contract's shapes (core/api/openapi.yaml, the staff-quote and
// staff-settings tags). In the served product no proxy exists: the app and
// the core share one origin and every call is same-origin relative, which is
// why the app's ApiClient is built with an empty base URL. The proxy port
// comes from QD_STUB_PORT so the Playwright harness and a hand-run dev
// server can point at the same stub.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

const stubPort = process.env.QD_STUB_PORT ?? "4839";
const appPort = process.env.QD_APP_PORT ?? "4838";

export default defineConfig(({ mode }) => ({
  // The seam rule (decision log rows 893, 899 and 906; the till's own config
  // on demo/front-door is the shape): the front door's handoff reader is
  // compiled in only on the demo build. Every other mode, dev included,
  // resolves the empty stub (src/demo-account.default.ts) in place of the
  // app's demo-account module, so the key name and the reader cannot enter
  // a default build's module graph whatever the tree shaker keeps;
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
  build: {
    outDir: "dist",
  },
}));
