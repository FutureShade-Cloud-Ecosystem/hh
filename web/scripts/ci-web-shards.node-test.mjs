// The shard assignment's own test (node --test). Every unit of work lands in
// exactly one shard, an unknown package still lands in a shard, and the real
// tree's assignment is complete for every shard count the workflow uses.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import {
  WEIGHTS,
  SPLITS,
  EVENING_GUARD,
  listPackages,
  buildUnits,
  assign,
  splitTestScript,
  validatePackages,
} from "./ci-web-shards.mjs";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

function fixtureTree(pkgs) {
  const root = mkdtempSync(join(tmpdir(), "ci-web-shards-"));
  for (const [rel, manifest] of Object.entries(pkgs)) {
    mkdirSync(join(root, rel), { recursive: true });
    writeFileSync(join(root, rel, "package.json"), JSON.stringify(manifest));
  }
  return root;
}

function placements(shards) {
  const seen = new Map();
  for (const shard of shards) {
    for (const unit of shard) seen.set(unit.id, (seen.get(unit.id) ?? 0) + 1);
  }
  return seen;
}

test("every unit lands in exactly one shard for every shard count", () => {
  const units = buildUnits(listPackages(webRoot), WEIGHTS, SPLITS);
  for (const count of [1, 2, 3, 6, 9, 40]) {
    const seen = placements(assign(units, count));
    assert.equal(seen.size, units.length, `count ${count}: a unit was dropped`);
    for (const unit of units) assert.equal(seen.get(unit.id), 1, `${unit.id} at ${count}`);
  }
});

test("every real package appears in the plan, split ones as playwright parts plus one extras unit", () => {
  const pkgs = listPackages(webRoot);
  const units = buildUnits(pkgs, WEIGHTS, SPLITS);
  for (const pkg of pkgs) {
    const parts = units.filter((u) => u.pkg === pkg.name);
    const split = SPLITS[pkg.name];
    assert.equal(parts.length, split ? split.parts + 1 : 1, pkg.name);
    const numbers = parts.map((u) => u.part).sort((a, b) => a - b);
    assert.deepEqual(numbers, split ? [0, ...Array.from({ length: split.parts }, (_, i) => i + 1)] : [1], pkg.name);
  }
  assert.ok(units.some((u) => u.id === EVENING_GUARD), "the evening guard unit is planned");
});

test("quote desk's typecheck, node tests and extra configs are a unit of their own, not part of a playwright part", () => {
  const units = buildUnits(listPackages(webRoot), WEIGHTS, SPLITS);
  const extras = units.filter((u) => u.pkg === "quote-desk" && u.part === 0);
  assert.equal(extras.length, 1);
  const parts = units.filter((u) => u.pkg === "quote-desk" && u.part > 0);
  assert.ok(parts.length >= 4, "quote desk's playwright run is split into at least four parts");
  assert.ok(Math.max(...parts.map((u) => u.weight)) <= 10, "no playwright part is planned over ten minutes");
});

test("an unknown package still lands in a shard", () => {
  const root = fixtureTree({
    "packages/brand-new": { name: "brand-new", scripts: { test: "node --test", typecheck: "tsc" } },
    "apps/quote-desk": { name: "quote-desk", scripts: { test: "playwright test", typecheck: "tsc" } },
  });
  try {
    const pkgs = listPackages(root);
    assert.deepEqual(pkgs.map((p) => p.name).sort(), ["brand-new", "quote-desk"]);
    const units = buildUnits(pkgs, WEIGHTS, SPLITS);
    for (const count of [1, 2, 5]) {
      const seen = placements(assign(units, count));
      assert.equal(seen.get("brand-new"), 1);
    }
  } finally {
    rmSync(root, { recursive: true });
  }
});

test("a weight table entry for a package that is gone is ignored, not planned", () => {
  const root = fixtureTree({ "apps/only": { name: "only", scripts: { test: "x", typecheck: "y" } } });
  try {
    const units = buildUnits(listPackages(root), { ...WEIGHTS, "ghost-app": 50 }, SPLITS);
    assert.ok(!units.some((u) => u.pkg === "ghost-app"));
  } finally {
    rmSync(root, { recursive: true });
  }
});

test("the packing is balanced: no shard far over the mean at six shards", () => {
  const units = buildUnits(listPackages(webRoot), WEIGHTS, SPLITS);
  const shards = assign(units, 6);
  const loads = shards.map((s) => s.reduce((a, u) => a + u.weight, 0));
  const total = loads.reduce((a, b) => a + b, 0);
  assert.ok(Math.max(...loads) <= total / 6 + Math.max(...units.map((u) => u.weight)), JSON.stringify(loads));
  assert.ok(Math.max(...loads) <= (total / 6) * 1.25, `the longest shard is far past the mean: ${JSON.stringify(loads)}`);
});

test("the assignment is deterministic", () => {
  const units = buildUnits(listPackages(webRoot), WEIGHTS, SPLITS);
  assert.deepEqual(assign(units, 6), assign(units, 6));
});

test("a split test script shards only the bare playwright run", () => {
  const script = "node --test a.mjs && playwright test && playwright test --config x.ts";
  const segments = splitTestScript(script);
  assert.deepEqual(segments, [
    { command: "node --test a.mjs", shardable: false },
    { command: "playwright test", shardable: true },
    { command: "playwright test --config x.ts", shardable: false },
  ]);
});

test("guards: ts sources without typecheck, test dir without test script, no suites", () => {
  const withTs = fixtureTree({ "apps/a": { name: "a", scripts: { test: "x" } } });
  const withDir = fixtureTree({ "apps/b": { name: "b", scripts: { typecheck: "y" } } });
  const none = fixtureTree({ "apps/c": { name: "c", scripts: { typecheck: "y" } } });
  try {
    writeFileSync(join(withTs, "apps/a/index.ts"), "export {};\n");
    mkdirSync(join(withDir, "apps/b/test"));
    assert.match(validatePackages(listPackages(withTs)).join("\n"), /a has TypeScript sources but no typecheck script/);
    assert.match(validatePackages(listPackages(withDir)).join("\n"), /b has a test directory but no test script/);
    assert.match(validatePackages(listPackages(none)).join("\n"), /no web package has a test suite/);
    assert.deepEqual(validatePackages(listPackages(webRoot)), []);
  } finally {
    for (const r of [withTs, withDir, none]) rmSync(r, { recursive: true });
  }
});
