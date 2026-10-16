// House Messages' acceptance run: two personas' story against the demo image itself (the built app
// under /messages/ and the demo core behind it), never the stub. They run in the demo round and in the
// image's own exit run.
//
// The image's address comes in as MSG_IMAGE_URL, and the gate's phrase as MSG_GATE_PHRASE (held by the
// runner's own shell; nothing committed knows one). Screens land in MSG_SHOT_DIR. One worker and file
// order: the world is real and shared, so the run is a story.
import { defineConfig } from "@playwright/test";

const baseURL = process.env.MSG_IMAGE_URL ?? "http://127.0.0.1:5801";

export default defineConfig({
  testDir: ".",
  testMatch: "acceptance/*.pw.ts",
  timeout: 150_000,
  workers: 1,
  use: {
    baseURL,
    channel: "chrome",
    headless: true,
    viewport: { width: 1440, height: 900 },
    httpCredentials: { username: "demo", password: process.env.MSG_GATE_PHRASE ?? "" },
  },
});
