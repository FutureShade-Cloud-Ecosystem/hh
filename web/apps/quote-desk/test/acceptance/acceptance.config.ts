// The Quote Desk's acceptance story against a REAL demo core (the demo image: the core, its database, its seeded quote
// world), never the stub. It is not part of `npm test`: it needs a booted demo box, and it consumes the seeded quote it
// converts, so every run wants a fresh world (see test/acceptance/README.md).
//
//   QD_ACCEPT_URL     the Quote Desk's address, serving the desk built or run in demo mode with /api on the demo box
//   QD_ACCEPT_USER    the box's gate user, when the box has the phrase gate on (the demo image: "demo")
//   QD_ACCEPT_PHRASE  the gate's phrase, from the environment only: this file never holds one and nothing prints it
import { defineConfig } from "@playwright/test";

const user = process.env.QD_ACCEPT_USER ?? "";
const phrase = process.env.QD_ACCEPT_PHRASE ?? "";

export default defineConfig({
  testDir: ".",
  testMatch: "**/*.acceptance.ts",
  timeout: 120_000,
  workers: 1,
  reporter: "line",
  use: {
    baseURL: process.env.QD_ACCEPT_URL ?? "http://127.0.0.1:4838/",
    ...(user !== "" && phrase !== "" ? { httpCredentials: { username: user, password: phrase } } : {}),
    channel: "chrome",
    headless: true,
    viewport: { width: 1440, height: 900 },
  },
});
