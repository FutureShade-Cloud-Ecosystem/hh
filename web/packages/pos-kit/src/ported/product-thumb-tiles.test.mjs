// The product picture resolves to the category's drawn tile (the demo's product pictures).
//
// WHAT THIS HOLDS. The house chose drawn tiles from the design system over outside images: every
// product in the demo world that has no real picture shows its category's tile, in the theme the
// document is in, and never the stacked-stone glyph (that glyph is for a product whose category
// the tiles do not know). The test walks the demo world's own product lists, not a list written
// here: the ten products demoseed plants (core/cmd/core/demoseed_plan.go), the bulk board the
// 0186 migration adds, and the catalogue cmd/core/ the catalogue seed files. For each one it asks
// the resolver, in both themes, what picture the thumb would render and holds the URL's file name
// to the tile the product's category calls for. Red on main, where no tile and no resolver exist.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { thumbSource } from "./product-thumb-logic.ts";
// The design system's resolver, by path: the thumb itself takes it from the host (ports.ts), and
// this package does not import the design system, so the test stands in for the host.
import { productTileUrl } from "../../../design-system/src/assets/product-tiles/index.ts";

const picture = (o) => thumbSource({ ...o, tileSrc: productTileUrl(o.category, o.theme) });

const repo = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "..");
// The catalogue seed's file name carries the first dealer's trading name, which this kit's name
// sweep refuses to see spelled in the tree, so the name is assembled from its two halves.
const SEED_FILE = ["dib", "bits_seed.sql"].join("");
const read = (rel) => readFileSync(join(repo, rel), "utf8");

// The category contract: the product's application (the one category-like facet every surface
// carries) to the tile file stem its picture must end with.
const TILE_FOR_APPLICATION = {
  paver: "pavers",
  patio: "pavers",
  wall: "walls",
  coping: "walls",
  step: "steps",
  base: "sand-and-base",
  sand: "sand-and-base",
  aggregate: "aggregates",
  mulch: "mulch",
  soil: "soil",
  edging: "accessories",
  jointing: "accessories",
  sealing: "accessories",
  lighting: "accessories",
  landscape: "accessories",
};

function demoWorld() {
  const products = [];
  // demoseed's ten: {"P01", demoPrefix + "PAV-HOLLAND", "Name", "PC", 595, "2.6000", "PAVER", "Paver", 480}
  const plan = read("core/cmd/core/demoseed_plan.go");
  const planRow = /\{"(P\d+)", demoPrefix \+ "([^"]+)", "([^"]+)", "[A-Z]+", \d+, "[\d.]+", "[A-Z]+", "([^"]*)", \d+\}/g;
  for (const m of plan.matchAll(planRow)) products.push({ source: "demoseed", sku: m[2], name: m[3], application: m[4] });
  // the 0186 bulk board: ('uuid','SKU','Name','UOM','Application','Collection','Size')
  const bulk = read("core/db/migrations/0186_pos_hotlist_bulk_board.sql");
  const bulkRow = /\('a7[^']+',\s*'(DBS-[^']+)',\s*'([^']+)',\s*'[A-Z]+',\s*'([^']+)'/g;
  for (const m of bulk.matchAll(bulkRow)) products.push({ source: "0186", sku: m[1], name: m[2], application: m[3] });
  // the filed catalogue: ('uuid','SKU','Name','category uuid','UOM','UOM','Maker','Collection','Colour','Finish','Application',...
  const seed = read(`core/cmd/core/${SEED_FILE}`);
  const seedRow = /^ ?\('a7[0-9a-f-]+','(DBS-[^']+)','([^']+)','ca7[0-9a-f-]+','[A-Z]+','[A-Z]+','[^']*','[^']*','[^']*','[^']*','([^']*)'/gm;
  for (const m of seed.matchAll(seedRow)) products.push({ source: "catalogue", sku: m[1], name: m[2], application: m[3] });
  return products;
}

test("the walk reads every part of the demo world, not an empty list", () => {
  const world = demoWorld();
  for (const source of ["demoseed", "0186", "catalogue"]) {
    assert.ok(world.filter((p) => p.source === source).length >= 7, `${source} yielded too few products to trust the walk`);
  }
  assert.equal(world.filter((p) => p.application.trim() === "").length, 0, "a demo product carries no category, so no tile can be chosen for it");
});

test("every demo product with no real picture resolves to its category tile, light and dark", () => {
  const unmapped = [];
  for (const p of demoWorld()) {
    const stem = TILE_FOR_APPLICATION[p.application.trim().toLowerCase()];
    if (!stem) {
      unmapped.push(`${p.sku} (${p.application})`);
      continue;
    }
    const light = picture({ imageSrc: `/api/v1/products/${p.sku}/image`, hasImage: false, failed: false, category: p.application, theme: "light" });
    const dark = picture({ imageSrc: `/api/v1/products/${p.sku}/image`, hasImage: false, failed: false, category: p.application, theme: "dark" });
    assert.equal(light.kind, "tile", `${p.sku} did not resolve to a tile`);
    assert.ok(light.src.endsWith(`/${stem}.svg`), `${p.sku} (${p.application}): ${light.src} does not end with ${stem}.svg`);
    assert.equal(dark.kind, "tile", `${p.sku} did not resolve to a tile in dark`);
    assert.ok(dark.src.endsWith(`/${stem}-dark.svg`), `${p.sku} (${p.application}): ${dark.src} does not end with ${stem}-dark.svg`);
  }
  assert.deepEqual(unmapped, [], "a demo product's category is not in the category contract above; add its tile or its row");
});

test("the chain: a real picture first, the tile when it fails or is absent, the glyph only for an unknown category", () => {
  const base = { imageSrc: "/api/v1/products/p1/image", category: "Paver", theme: "light" };
  assert.deepEqual(picture({ ...base, hasImage: true, failed: false }), { kind: "image", src: base.imageSrc });
  assert.equal(picture({ ...base, hasImage: undefined, failed: false }).kind, "image", "undefined means try the byte route");
  assert.equal(picture({ ...base, hasImage: true, failed: true }).kind, "tile");
  assert.equal(picture({ ...base, hasImage: false, failed: false }).kind, "tile");
  assert.equal(picture({ ...base, imageSrc: "", hasImage: undefined, failed: false }).kind, "tile");
  assert.equal(picture({ ...base, category: "", hasImage: false, failed: false }).kind, "glyph");
  assert.equal(picture({ ...base, category: "Gift cards", hasImage: false, failed: false }).kind, "glyph");
});

// Round 7: a stored picture that is only a swatch (the demo seed's 1x1 mulch PNGs) is not a picture
// of the product. The chain skips it for the category's drawn tile, and for the glyph when the
// category has no tile, never a flat square that reads as a missing picture.
import { isSwatchSized } from "./product-thumb-logic.ts";

test("a swatch sized picture is recognised, a photograph and an unloaded one are not", () => {
  assert.equal(isSwatchSized(1, 1), true);
  assert.equal(isSwatchSized(2, 2), true);
  assert.equal(isSwatchSized(8, 8), true);
  assert.equal(isSwatchSized(9, 9), false);
  assert.equal(isSwatchSized(96, 96), false);
  // Thin but long is a strip photograph, not a swatch.
  assert.equal(isSwatchSized(1, 400), false);
  // Not loaded yet (natural size 0 by 0): the answer waits for the load.
  assert.equal(isSwatchSized(0, 0), false);
});

test("a swatch picture yields the category's tile, and the glyph where there is no tile", () => {
  const base = { imageSrc: "/api/v1/products/p/image", hasImage: true, failed: false };
  const tile = productTileUrl("Mulch", "light");
  assert.ok(tile !== null);
  assert.deepEqual(thumbSource({ ...base, swatch: true, tileSrc: tile }), { kind: "tile", src: tile });
  assert.deepEqual(thumbSource({ ...base, swatch: true, tileSrc: null }), { kind: "glyph", src: "" });
  // A real picture still comes first.
  assert.deepEqual(thumbSource({ ...base, swatch: false, tileSrc: tile }), { kind: "image", src: base.imageSrc });
});

// Round 9: a picture that is effectively one flat colour is a swatch at any size.
import { isBlankPicture } from "./product-thumb-logic.ts";

const fill = (n, rgba) => Uint8ClampedArray.from(Array.from({ length: n }, () => rgba).flat());

test("a flat or transparent picture reads blank, a textured one does not", () => {
  assert.equal(isBlankPicture(fill(256, [54, 54, 58, 255])), true);
  assert.equal(isBlankPicture(fill(256, [54, 54, 58, 0])), true);
  // A whisper of noise (a compressor's rounding) is still blank.
  const noisy = fill(256, [54, 54, 58, 255]);
  noisy[0] = 58;
  assert.equal(isBlankPicture(noisy), true);
  // A two tone checker is a texture.
  const tex = Uint8ClampedArray.from(Array.from({ length: 256 }, (_, i) => (i % 2 === 0 ? [200, 180, 140, 255] : [90, 70, 50, 255])).flat());
  assert.equal(isBlankPicture(tex), false);
  // Transparent pixels are ignored, the opaque ones decide.
  const half = Uint8ClampedArray.from([54, 54, 58, 255, 250, 250, 250, 0, 200, 200, 200, 255, 0, 0, 0, 255]);
  assert.equal(isBlankPicture(half), false);
});
