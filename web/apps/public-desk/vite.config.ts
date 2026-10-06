// The Public Desk's Vite configuration: one dev server and one build output
// for this one micro-app (web/apps/README.md: one Vite entry per micro-app).
//
// The dev server proxies /api to a local stub so the page's calls to the
// public quote routes answer without a running core: the stub
// (test/stub/public-stub.mjs) serves the share link reads and the accept in
// the contract's shapes (core/api/openapi.yaml, QuotePublicView and
// QuoteAcceptanceResult) plus the product image read. In the served product
// no proxy exists: the page and the core share one origin and every call is
// same-origin relative, which is why the page's ApiClient is built with an
// empty base URL and no header provider at all: a customer on a share link
// holds no credential to attach. The proxy port comes from PD_STUB_PORT so
// the Playwright harness and a hand-run dev server can point at the same
// stub.
import { defineConfig } from "vite";

const stubPort = process.env.PD_STUB_PORT ?? "4837";
const appPort = process.env.PD_APP_PORT ?? "4836";

export default defineConfig({
  server: {
    port: Number(appPort),
    strictPort: true,
    host: "127.0.0.1",
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      // The stub's own peek route (test scaffolding, no contract path shares
      // the namespace): proxied so the suite reads the stub's request ledger
      // through the same origin the page uses.
      "/_stub": {
        target: `http://127.0.0.1:${stubPort}`,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
