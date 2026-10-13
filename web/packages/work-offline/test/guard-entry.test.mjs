// The guard entry: the apps that are online only (Money Desk, Dealer Admin) and
// the ones whose money actions only need the refusal (Manager Home, Buying and
// Stock) import the no signal guard WITHOUT the offline store. The entry is
// the signal module alone, and its import graph holds no store, no sync driver
// and no wa-sqlite, so an online only app ships none of them.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as guard from "../src/guard.ts";
import * as signal from "../src/signal.ts";

const src = join(dirname(fileURLToPath(import.meta.url)), "..", "src");
const pkg = JSON.parse(readFileSync(join(src, "..", "package.json"), "utf8"));

test("the guard entry is the signal module and nothing else", () => {
  assert.deepEqual(Object.keys(guard).sort(), Object.keys(signal).sort());
  for (const name of ["NO_SIGNAL_ERROR", "browserSignal", "noSignalFetch", "noSignalWebFetch", "onlineOnly", "isNoSignal", "fixedSignal"]) assert.ok(name in guard, name);
});

test("the package exports the entry as ./guard", () => {
  assert.equal(pkg.exports["./guard"], "./src/guard.ts");
});

test("the guard's import graph holds no offline store, no sync driver and no sqlite", () => {
  for (const file of ["guard.ts", "signal.ts"]) {
    const text = readFileSync(join(src, file), "utf8");
    const imports = [...text.matchAll(/(?:import|export)[^;]*?from\s+"([^"]+)"/g)].map((m) => m[1]);
    for (const spec of imports) assert.ok(spec === "@hardscapehouse/api-client" || spec === "./signal.js", `${file} imports ${spec}`);
    assert.doesNotMatch(text, /offline-store|wa-sqlite|statecharts/);
  }
});
