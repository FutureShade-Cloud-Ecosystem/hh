// The test harness never pins a port (RULES-S4 addendum 7: a test server
// never pins one, because a pinned one collides by chance with whatever the
// system hands out at that moment). The stub and the manual shot drivers take
// their ports from the caller; the configs already hand out free ones. This
// guard reads the harness sources and fails when a fixed default sneaks back.
// The dev server's own defaults in vite.config.ts stay: a long running
// development server outside the test harness is the one allowed default.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = dirname(fileURLToPath(import.meta.url));

const sources = [
  { path: join(dir, "stub", "demo-stub.mjs"), label: "test/stub/demo-stub.mjs", vars: ["FD_STUB_PORT"] },
  { path: join(dir, "acceptance", "shots.mjs"), label: "test/acceptance/shots.mjs", vars: ["FD_APP_PORT"] },
  {
    path: join(dir, "acceptance", "approvals-note-shots.mjs"),
    label: "test/acceptance/approvals-note-shots.mjs",
    vars: ["FD_APP_PORT", "FD_STUB_PORT"],
  },
];

test("the stub and the manual drivers take their ports from the caller, never a default", () => {
  for (const { path, label, vars } of sources) {
    const src = readFileSync(path, "utf8");
    assert.equal(
      /\?\?\s*"?[0-9]{2,5}"?/.test(src),
      false,
      `${label} carries a fixed port default; take the port from the caller`,
    );
    for (const name of vars) {
      assert.ok(src.includes(`process.env.${name}`), `${label} reads ${name} from the caller`);
    }
  }
});
