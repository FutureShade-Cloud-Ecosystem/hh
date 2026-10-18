// The console legs for this app (item 7d): the page recovers its own session in console mode, behind the
// shared console harness (packages/pos-kit/test/console-harness.ts). `npx playwright test --config
// test/console.config.ts`, on the five ports from CW_FIRST_PORT.
import { fileURLToPath } from "node:url";
import { consoleHarness } from "../../../packages/pos-kit/test/console-harness.js";

const appDir = fileURLToPath(new URL("..", import.meta.url));

export default consoleHarness({
  appDir,
  appStub: { command: "node test/stub/manager-stub.mjs", portEnv: "MH_STUB_PORT" },
  appVite: { stubPortEnv: "MH_STUB_PORT", appPortEnv: "MH_APP_PORT" },
  firstPort: 28180,
});
