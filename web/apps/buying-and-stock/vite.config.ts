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

const stubPort = process.env.ROLE_STUB_PORT ?? "4969";
const appPort = process.env.ROLE_APP_PORT ?? "4968";
// The box's inotify slots are shared with every other lane's dev servers, and when they are all
// taken a dev server dies at startup (EMFILE on the first watch). ROLE_VITE_NO_WATCH=1 starts the
// server watching nothing, which a headless leg run never needs; the default watches as always.
const noWatch = process.env.ROLE_VITE_NO_WATCH === "1";

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
    ...(noWatch ? { watch: { ignored: "**" } } : {}),
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
