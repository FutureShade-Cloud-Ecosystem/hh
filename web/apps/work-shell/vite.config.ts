// The Work shell host's Vite configuration: one bundle, served from the
// origin root (the host IS the Work profile's entry page; the front door sits
// at /door/ and each desk app at its own path beside it, laid out by
// tauri/hh-work/assemble-dist.sh). The dev server binds a port the system
// picks unless one is given, and has no proxy: the host reads two static
// files (profile.json, launch.json) from its own origin.
import { defineConfig } from "vite";

export default defineConfig({
  base: "/",
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WS_APP_PORT ?? 0),
  },
  build: {
    outDir: "dist",
  },
});
