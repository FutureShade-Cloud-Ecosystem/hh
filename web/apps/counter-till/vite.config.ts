// The Counter Till's Vite configuration: one dev server and one build output
// for this one micro-app (web/apps/README.md: one Vite entry per micro-app).
//
// The dev server proxies /api to a local demo stub so the app's calls to the
// demo routes answer without a running core: the stub (test/stub/demo-stub.mjs)
// serves the two flows and the session mint in the contract's shapes
// (core/api/openapi.yaml, DemoFlowList and DemoSessionResponse). In the served
// product no proxy exists: the app and the core share one origin and every
// call is same-origin relative, which is why the app's ApiClient is built with
// an empty base URL. The proxy port comes from CT_STUB_PORT so the Playwright
// harness and a hand-run dev server can point at the same stub.
import { defineConfig } from "vite";

const stubPort = process.env.CT_STUB_PORT ?? "4827";
const appPort = process.env.CT_APP_PORT ?? "4826";

export default defineConfig({
  server: {
    port: Number(appPort),
    strictPort: true,
    host: "127.0.0.1",
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${stubPort}`,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
