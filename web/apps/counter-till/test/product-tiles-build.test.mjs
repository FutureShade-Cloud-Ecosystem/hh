// The drawn product tiles are a demo seam for now (decision log row 893, the same seam as the
// adopt path): the till wires the design system's tile resolver into the ports only in a demo
// mode build. The proof is the shipped bundle, because the Vite mode gate is constant folded at
// build time. This test builds the app both ways into its own scratch directory and looks for the
// resolver's own vocabulary and the tiles' drawings (the small SVGs are inlined as data URIs, so
// their text, not a file name, is what a build carries): a default build holds none of it, so a
// product with no picture keeps today's glyph; a demo build holds all of it.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const scratchDir = join(appDir, "test", ".product-tiles-build");

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

test("a default build of the till carries no tile resolver and no tile drawing", () => {
  const bundle = build("default", []);
  assert.ok(bundle.length > 0, "the default build produced nothing");
  assert.ok(!bundle.includes(RESOLVER_WORD), "the default bundle carries the tile resolver");
  assert.ok(!TILE_PLATE.test(bundle), "the default bundle carries a tile drawing");
});

test("a demo mode build of the till carries the tile resolver and the tile drawings", () => {
  const bundle = build("demo", ["--mode", "demo"]);
  assert.ok(bundle.includes(RESOLVER_WORD), "the demo bundle carries no tile resolver");
  assert.ok(TILE_PLATE.test(bundle), "the demo bundle carries no tile drawing");
});

test("the tile resolver is wired only inside the demo build mode gate, at source", () => {
  const source = readFileSync(join(appDir, "src", "app-root.ts"), "utf8").replace(/\s+/g, " ");
  assert.ok(
    source.includes('productTile: import.meta.env.MODE === "demo" ? productTileUrl : undefined'),
    "app-root must wire productTile only when the build mode is demo",
  );
});

after(() => {
  rmSync(scratchDir, { recursive: true, force: true });
});
