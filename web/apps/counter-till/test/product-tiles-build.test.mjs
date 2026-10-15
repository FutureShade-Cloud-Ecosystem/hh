// The drawn product tiles ship in every build (decision log row 1057: any product without a photo
// shows its category's drawn tile in every dealer's system, not the demo only). The proof is the
// shipped bundle, because a bundler may drop what no path reaches: this test builds the app both
// ways into its own scratch directory and looks for the resolver's own vocabulary and the tiles'
// drawings (the small SVGs are inlined as data URIs, so their text, not a file name, is what a
// build carries). Both a default build and a demo build hold all of it. The demo-only seams (the
// front door handoff and its key) stay out of the default build: see no-default-adopt.test.mjs.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
// A fresh directory under the OS temp dir, never under the package: the kits raw markup guard walks every
// dependent's folder and a built bundle sitting there while it walks (or left by a killed run) fails the drift gate.
const scratchDir = mkdtempSync(join(tmpdir(), "hh-product-tiles-"));

function* files(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else yield path;
  }
}

function build(label, args) {
  const outDir = join(scratchDir, label);
  const run = spawnSync("npx", ["--no-install", "vite", "build", "--outDir", outDir, ...args], {
    cwd: appDir,
    encoding: "utf8",
    timeout: 300_000,
  });
  assert.equal(run.status, 0, `the ${label} build failed:\n${run.stdout}\n${run.stderr}`);
  return [...files(outDir)].map((path) => readFileSync(path, "utf8")).join("\n");
}

// One word only the resolver's table carries, and the plate every tile draws (a circle of radius
// 38; a data URI carries the svg percent encoded or quoted differently, so the pattern takes each).
const RESOLVER_WORD = "screenings";
const TILE_PLATE = /fill-opacity=(%22|"|')0\.94/;

test("a default build of the till carries the tile resolver and the tile drawings", () => {
  const bundle = build("default", []);
  assert.ok(bundle.length > 0, "the default build produced nothing");
  assert.ok(bundle.includes(RESOLVER_WORD), "the default bundle carries no tile resolver");
  assert.ok(TILE_PLATE.test(bundle), "the default bundle carries no tile drawing");
});

test("a demo mode build of the till carries the tile resolver and the tile drawings", () => {
  const bundle = build("demo", ["--mode", "demo"]);
  assert.ok(bundle.includes(RESOLVER_WORD), "the demo bundle carries no tile resolver");
  assert.ok(TILE_PLATE.test(bundle), "the demo bundle carries no tile drawing");
});

test("the tile resolver is wired unconditionally, at source", () => {
  const source = readFileSync(join(appDir, "src", "app-root.ts"), "utf8").replace(/\s+/g, " ");
  assert.ok(source.includes("productTile: productTileUrl,"), "app-root must wire productTile in every build");
  assert.ok(!/productTile: [^,]*MODE/.test(source), "the tile resolver must not sit behind a build mode gate");
});

after(() => {
  rmSync(scratchDir, { recursive: true, force: true });
});
