// Vite configuration for Pro Field.
//
// The dev server proxies /api and /_stub to the shared tenant stub
// (web/packages/pro-kit/test-stub/tenant-stub.mjs) so the app's calls answer
// without a running core. The stub port comes from ROLE_STUB_PORT so the
// Playwright harness and a hand-run dev server point at the same stub. The
// defaults are this app's own pair in the Pro cycle's range.
import { defineConfig } from "vite";

const stubPort = process.env.ROLE_STUB_PORT ?? "56830";
const appPort = process.env.ROLE_APP_PORT ?? "56831";

export default defineConfig({
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
