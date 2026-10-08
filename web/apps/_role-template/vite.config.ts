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
// without a running core. The stub port comes from ROLE_STUB_PORT so the
// Playwright harness and a hand-run dev server can point at the same stub.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

const stubPort = process.env.ROLE_STUB_PORT ?? "4939";
const appPort = process.env.ROLE_APP_PORT ?? "4938";

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
