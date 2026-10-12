// Manager Home's acceptance run: the manager's story against the demo image
// itself (the built app under /home/ and the demo core behind it), never the
// stub. THEY DO NOT RUN IN THE BUILD LANE. They run in the demo round, after
// the yard manager's demo mint row and the Manager Home app are in the image.
//
// The image's address comes in as MH_IMAGE_URL, and the gate's phrase as
// MH_GATE_PHRASE (the exit script holds it in its own shell; nothing
// committed knows one). Screens land in MH_SHOT_DIR. One worker and file
// order: the world is real and shared, so the run is a story with a
// beginning (the card lands, the queue counts the seed) and an end (a
// shortage is resolved).
import { defineConfig } from "@playwright/test";

const baseURL = process.env.MH_IMAGE_URL ?? "http://127.0.0.1:5811";

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
    httpCredentials: { username: "demo", password: process.env.MH_GATE_PHRASE ?? "" },
  },
});
