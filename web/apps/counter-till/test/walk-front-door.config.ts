// The front door walk's Playwright configuration: the walk spec against a
// running box of the assembled demo image (staging, or a local proof box of
// the same image), not against the stub harness and not against the till
// exit image: the walk opens the front door and the Quote Desk, which only
// the assembled box serves. The box's address arrives as CT_IMAGE_URL and
// the gate phrase as CT_GATE_PHRASE, the same variables the acceptance
// suite reads (the runner carries the phrase; nothing committed knows one),
// and the browser context answers the gate's basic authentication challenge
// with them. No default box is named on purpose: a walk pointed at nothing
// fails at its first navigation, never at a stranger's address.
//
// This config sits beside acceptance.config.ts and its specs live one level
// below the acceptance specs, in test/acceptance/walk/: the default
// config's testIgnore covers everything under acceptance/, the acceptance
// config's match (acceptance/*.pw.ts) stops one level short, and the
// popout and default-build configs name their own files, so the walk runs
// only when this config is named, and never in the default legs (it needs
// the demo image).
import { defineConfig } from "@playwright/test";

const baseURL = process.env.CT_IMAGE_URL ?? "";

export default defineConfig({
  testDir: "./acceptance/walk",
  testMatch: "**/*.pw.ts",
  // The walk signs in five times at the box's mint pace (ten seconds of
  // spacing between mints, and the belt's window wait on a refusal), and
  // walks two modules and the role home between them. 180000ms holds the
  // slowest single walk with margin for a staging box's round trips.
  timeout: 180_000,
  workers: 1,
  use: {
    baseURL,
    channel: "chrome",
    headless: true,
    httpCredentials: { username: "demo", password: process.env.CT_GATE_PHRASE ?? "" },
  },
});
