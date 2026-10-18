// The console legs for this app (item 7d): the page recovers its own session in console mode, behind the
// shared console harness (packages/pos-kit/test/console-harness.ts). `npx playwright test --config
// test/console.config.ts`, on the five ports from CW_FIRST_PORT.
import { fileURLToPath } from "node:url";
import { consoleHarness } from "../../../packages/pos-kit/test/console-harness.js";

const appDir = fileURLToPath(new URL("..", import.meta.url));

export default consoleHarness({
  appDir,
  appStub: { command: "node test/stub/demo-stub.mjs", portEnv: "CT_STUB_PORT" },
  appVite: { stubPortEnv: "CT_STUB_PORT", appPortEnv: "CT_APP_PORT" },
  firstPort: 28170,
});
