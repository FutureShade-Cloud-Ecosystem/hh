// Serves the package root, so the harness page can import /src/shell-rail.ts.
import { defineConfig } from "vite";
import { fileURLToPath } from "node:url";

export default defineConfig({
  root: fileURLToPath(new URL("../../", import.meta.url)),
  server: { fs: { strict: false }, watch: null },
});
