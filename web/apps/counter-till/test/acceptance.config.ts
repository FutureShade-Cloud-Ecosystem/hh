// The W9 acceptance run's Playwright configuration: the eleven screens of the
// plan's section 2, driven as each role against the demo image itself (the
// built app and the demo core behind it), not against the dev stub. The image
// serves the app behind the phrase gate, so the run carries the phrase as
// CT_GATE_PHRASE (the exit script generates it, holds it in its own shell and
// passes it here; nothing committed knows a phrase), and the browser context
// answers the gate's basic authentication challenge with it.
//
// The image's address comes in as CT_IMAGE_URL (the exit script's box), the
// screenshots land in CT_SHOT_DIR (a clearly named untracked folder, produced
// on demand by the run), and the restart leg receives the pre restart token as
// CT_PRIOR_TOKEN. One worker, tests in file order: the world is real and
// shared, so the run is a story with a beginning and an end.
import { defineConfig } from "@playwright/test";

const baseURL = process.env.CT_IMAGE_URL ?? "http://127.0.0.1:5801";

export default defineConfig({
  // This config sits inside the acceptance directory, so the test dir is here
  // and the match is the acceptance specs alone: the stub harness's suites
  // belong to the default config and its own web servers.
  testDir: ".",
  testMatch: "acceptance/*.pw.ts",
  // The test budget is written here, not inherited: the worst honest test
  // carries its own work (the work fits the suite's old 30000ms default, kept
  // as the work allowance), at most three paces (the parked sales spec signs
  // in three times: three spacings of 10000ms, 30000ms), and the belt's worst
  // recovery should a refusal actually fire (two window waits of 31000ms,
  // 62000ms; the retry taps after a wait pace for nothing). The sum is
  // 122000ms, and the budget rounds it up to 150000ms so a staging box's
  // slower round trips spend the margin, not the test.
  timeout: 150_000,
  workers: 1,
  use: {
    baseURL,
    channel: "chrome",
    headless: true,
    httpCredentials: { username: "demo", password: process.env.CT_GATE_PHRASE ?? "" },
  },
});
