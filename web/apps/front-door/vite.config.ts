// The front door's Vite configuration: one dev server and one build output
// for this one micro-app (web/apps/README.md: one Vite entry per micro-app),
// served from the origin root (base "/": on the demo box this app IS the
// box's first screen, and the till and the quote desk hang off /till/ and
// /quote/ beside it).
//
// The dev server proxies /api to the local demo stub (test/stub/demo-stub.mjs)
// so the app's mint call answers without a running core; the served product
// has no proxy (the app, the core and the gate share one origin, which is why
// the app's ApiClient is built with an empty base URL). The ports come from
// FD_STUB_PORT and FD_APP_PORT so this app's harness and the Counter Till's
// (CT_STUB_PORT and CT_APP_PORT) never fight over a port on one machine.
import { defineConfig } from "vite";

const stubPort = process.env.FD_STUB_PORT ?? "4927";
const appPort = process.env.FD_APP_PORT ?? "4926";

export default defineConfig({
  base: "/",
  server: {
    port: Number(appPort),
    strictPort: true,
    host: "127.0.0.1",
    // The set password page's link carries a one time secret in its query. No
    // request the browser makes from that page may carry a Referer, or the
    // secret reaches another server's log. The dev server's own header stands
    // in for the deployed copy's Caddyfile line: the document's meta tag is
    // the page's own layer, and the header is the layer the server controls.
    headers: { "Referrer-Policy": "no-referrer" },
    proxy: {
      "/api": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      // The stub's own peek and refusal controls (test scaffolding; no
      // contract path shares the namespace): proxied so the suite reads the
      // stub's ledgers through the same origin the app uses.
      "/_stub": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      // The served runtime configuration and the console mode's fake FB
      // Console (test scaffolding): same origin as the page, so the
      // credentialed fetches need no CORS and the cookie is first party.
      "/config.json": {
        target: `http://127.0.0.1:${stubPort}`,
      },
      "/console": {
        target: `http://127.0.0.1:${stubPort}`,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
