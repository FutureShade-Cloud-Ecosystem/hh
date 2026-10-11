// Yard Pick's acceptance run: the yard team's story against the demo image
// itself (the built app under /yard/ and the demo core behind it), never the
// stub. THEY DO NOT RUN IN THE BUILD LANE. They run in the demo round, after
// the Yard Team's demo mint row (PR 274) and the cost fence on the yard's
// stock reads (PR 275) are in the image. THIS MACHINE CANNOT BUILD THE DEMO
// IMAGE, so these legs are written here and proved on HQ.
//
// The image's address comes in as YARD_IMAGE_URL, and the gate's phrase as
// YARD_GATE_PHRASE (the exit script holds it in its own shell; nothing
// committed knows one). Screens land in YARD_SHOT_DIR. One worker and file
// order: the world is real and shared.
import { defineConfig } from "@playwright/test";

const baseURL = process.env.YARD_IMAGE_URL ?? "http://127.0.0.1:5801";

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
    httpCredentials: { username: "demo", password: process.env.YARD_GATE_PHRASE ?? "" },
  },
});
