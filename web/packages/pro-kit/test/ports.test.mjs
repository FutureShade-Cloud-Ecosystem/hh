// The Pro apps' test servers never share a port. A node leg starts the shared stub through spawnStub (an
// ephemeral port the stub reports itself, ended by its own exit door and awaited), never a probed fixed port; and the
// fixed pairs the Playwright and Vite configs default to are all distinct, so the suites of the three apps and the kit
// can run at once (CI's loop runs them one after another; a root run and a lane's run overlap).
import assert from "node:assert/strict";
import { readdirSync, readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const web = join(fileURLToPath(new URL(".", import.meta.url)), "..", "..", "..");
const proDirs = ["packages/pro-kit", "apps/pro-procurement", "apps/pro-pay", "apps/pro-field"];

function filesUnder(dir, keep, out = []) {
  if (!existsSync(dir)) return out;
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === "node_modules") continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) filesUnder(p, keep, out);
    else if (keep(p)) out.push(p);
  }
  return out;
}

test("no Pro node test probes a fixed port range for the stub", () => {
  const offenders = [];
  for (const d of proDirs) {
    for (const f of filesUnder(join(web, d, "test"), (p) => p.endsWith(".test.mjs"))) {
      if (f === fileURLToPath(import.meta.url)) continue;
      const src = readFileSync(f, "utf8");
      if (/freePort\(|ROLE_STUB_PORT:\s*String\(|child_process/.test(src)) offenders.push(f.slice(web.length + 1));
    }
  }
  assert.deepEqual(offenders, [], "start the stub through spawnStub from @hardscapehouse/pro-kit/test-stub/spawn-stub.mjs");
});

test("every Pro Playwright config takes its ports from the system, never a fixed pair", () => {
  const configs = proDirs.flatMap((d) => filesUnder(join(web, d), (p) => /(playwright|acceptance|live)\.config\.ts$/.test(p)));
  assert.ok(configs.length >= 5, "the Playwright configs were found");
  const fixed = configs.filter((f) => !/await freePorts\(2\)/.test(readFileSync(f, "utf8")) || /\?\?\s*"\d+"/.test(readFileSync(f, "utf8")));
  assert.deepEqual(fixed.map((f) => f.slice(web.length + 1)), []);
});

test("the Vite dev defaults of the three apps stay distinct", () => {
  const owners = new Map();
  for (const d of proDirs) {
    for (const f of filesUnder(join(web, d), (p) => /vite\.config\.ts$/.test(p))) {
      for (const m of readFileSync(f, "utf8").matchAll(/\?\?\s*"(\d+)"/g)) {
        if (!owners.has(m[1])) owners.set(m[1], new Set());
        owners.get(m[1]).add(d);
      }
    }
  }
  assert.ok(owners.size >= 6, "the Vite default ports were found");
  assert.deepEqual([...owners].filter(([, dirs]) => dirs.size > 1), []);
});
