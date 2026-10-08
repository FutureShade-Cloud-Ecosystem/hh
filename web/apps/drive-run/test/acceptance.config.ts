// HH Drive's acceptance run: the driver's story against the demo image itself
// (the built app under /drive/ and the demo core behind it), never the stub.
// THEY DO NOT RUN IN THE BUILD LANE. They run in the demo round, after the
// Driver's demo mint row and the runs and stops sync fence (built on the HH
// workstation branch) are in the image.
//
// The image's address comes in as DRIVE_IMAGE_URL, and the gate's phrase as
// DRIVE_GATE_PHRASE (the exit script holds it in its own shell; nothing
// committed knows one). Screens land in DRIVE_SHOT_DIR. One worker and file
// order: the world is real and shared, so the run is a story with a beginning
// (the run is dispatched, nothing delivered) and an end (the run is closed).
import { defineConfig } from "@playwright/test";

const baseURL = process.env.DRIVE_IMAGE_URL ?? "http://127.0.0.1:5801";

export default defineConfig({
  testDir: ".",
  testMatch: "acceptance/*.pw.ts",
  timeout: 150_000,
  workers: 1,
  use: {
    baseURL,
    channel: "chrome",
    headless: true,
    viewport: { width: 390, height: 844 },
    httpCredentials: { username: "demo", password: process.env.DRIVE_GATE_PHRASE ?? "" },
  },
});
