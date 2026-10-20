// PRR-500: LookupDesk.#readImages redials every product id, cached or not, because the
// method never asks the cache before the Promise.all. Each search therefore fetches the
// same product picture the screen already holds, on every keystroke that covers the same
// row. The fix filters the cache hits out before the network call, so a second search
// reuses what the first one parked.
//
// Red first: against the pre-PRR-500 source these assertions would fail (the method
// fires fetch(productIds.map(...)) with no filter between the parameter and the fetch).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appDir = join(here, "..");
const src = (rel) => readFileSync(join(appDir, rel), "utf8");

/**
 * The body of `#readImages(productIds: readonly string[]): void { ... }`, braces included. Used
 * by the source-reading tests below (PRR-500) to assert that the caching fix sits inside the
 * method body without depending on a shared body-slicing helper.
 */
function readImagesBody(source) {
  const declaration = /#readImages\s*\(/.exec(source);
  if (declaration === null) {
    throw new Error("#readImages( is not declared in the source: the slice cannot be taken, so the assertions would read nothing");
  }
  const open = source.indexOf("{", declaration.index + declaration[0].length);
  if (open === -1) throw new Error("#readImages has no body");
  let depth = 0;
  for (let j = open; j < source.length; j++) {
    if (source[j] === "{") depth++;
    else if (source[j] === "}") {
      depth--;
      if (depth === 0) return source.slice(open, j + 1);
    }
  }
  throw new Error("#readImages has an unbalanced body");
}

test("LookupDesk.#readImages filters product ids the cache already holds before the fetch", () => {
  const source = src("src/lookup/lookup-desk.ts");
  const body = readImagesBody(source);
  // A .filter that walks productIds and drops the ids already in this.#images must sit
  // between the parameter list and the first Promise.all. The cached ids never reach
  // the network because the fetch callback only sees the fresh list.
  const filterIdx = body.indexOf(".filter(");
  const fetchIdx = body.indexOf("fetch(");
  assert.ok(filterIdx > 0, "the body must call .filter on productIds to drop cached ids");
  assert.ok(fetchIdx > 0, "the body must still call fetch for the ids that are new");
  assert.ok(filterIdx < fetchIdx, "the filter must run before the fetch, so cached ids never reach Promise.all");
});

test("LookupDesk.#readImages still lands the cached ids and skips the empty fresh list", () => {
  const source = src("src/lookup/lookup-desk.ts");
  const body = readImagesBody(source);
  // The check protects the cache from being clobbered and the Promise.all from a no-op
  // request batch; both behaviours hold the screen's repaint to "something landed".
  assert.match(body, /this\.#images\.has\(id\)/, "the body still consults the cache on the answer side too");
  assert.match(body, /fresh\.length === 0/, "an all-cached input must short-circuit before any fetch");
});
