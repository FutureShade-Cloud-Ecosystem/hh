// manager-home and the Work shell's no signal rule. Money never queues (plan B.3, D5):
// every money moving action, every approval and every price change resolves
// online with the honest "no signal" refusal and is never stored. The rule is
// held where the app's client is built: its staff client rides the shell's
// guard, so every write the app makes with no signal is refused before it
// dials. The guard is imported from its own entry, so this app ships no offline
// store, no sync driver and no sqlite engine (the acceptance leg, offline.pw.ts,
// drives a real money action with the network gone).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

function sources(dir = srcDir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (name.endsWith(".ts")) out.push([path, readFileSync(path, "utf8")]);
  }
  return out;
}

test("the staff client is built on the no signal guard", () => {
  const root = readFileSync(join(srcDir, "app-root.ts"), "utf8");
  assert.match(root, /import \{[^}]*\bbrowserSignal\b[^}]*\bnoSignalFetch\b[^}]*\} from "@hardscapehouse\/work-offline\/guard";/);
  const staff = /#staffClient = new ApiClient\(\{[\s\S]*?\n  \}\);/.exec(root);
  assert.ok(staff, "the staff client is found");
  assert.match(staff[0], /fetch: noSignalFetch\(undefined, browserSignal\(\)\)/);
});

test("the demo way in is the one client without the guard: it is pre auth and the app is not signed in yet", () => {
  const root = readFileSync(join(srcDir, "app-root.ts"), "utf8");
  const demo = /#demoClient = new ApiClient\(\{[\s\S]*?\n  \}\);/.exec(root);
  assert.ok(demo);
  assert.doesNotMatch(demo[0], /noSignalFetch/);
});

test("no file of src reaches the offline store: only the guard entry of the package is imported", () => {
  for (const [path, text] of sources()) {
    assert.doesNotMatch(text, /@hardscapehouse\/offline-store/, path);
    for (const m of text.matchAll(/from "(@hardscapehouse\/work-offline[^"]*)"/g)) assert.equal(m[1], "@hardscapehouse/work-offline/guard", path);
  }
});
