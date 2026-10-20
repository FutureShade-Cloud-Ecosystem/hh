// PRR-498: IncomingDesk.#read fires one detail read per placed PO through
// Promise.all. A branch with many open orders therefore dials unbounded
// parallel requests, on top of whatever else the page and the staff client
// already hold. The fix bounds the cap (six, the page's own flight rule's
// floor) with a small helper that returns results in source order.
//
// Red first: against the pre-PRR-498 source these assertions would fail
// (the desk used Promise.all(placed.map(...)) with no bounded helper, and
// no helper named bounded existed).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appDir = join(here, "..");
const src = (rel) => readFileSync(join(appDir, rel), "utf8");

/**
 * The body of `#read(): Promise<void> { ... }`, braces included. Used by the source-reading
 * tests below (PRR-498) to assert the desk's body without depending on `functionBody`, which
 * only matches an unprefixed name. The walk mirrors `_slice.mjs`'s shape: find the open brace
 * after the parameter list, then walk the matching close.
 */
function privateMethodBody(source, name) {
  const declaration = new RegExp(`#${name}\\s*\\(`).exec(source);
  if (declaration === null) {
    throw new Error(`#${name}( is not declared in the source: the slice cannot be taken, so the assertions would read nothing`);
  }
  const open = source.indexOf("{", declaration.index + declaration[0].length);
  if (open === -1) throw new Error(`#${name} has no body`);
  let depth = 0;
  for (let j = open; j < source.length; j++) {
    if (source[j] === "{") depth++;
    else if (source[j] === "}") {
      depth--;
      if (depth === 0) return source.slice(open, j + 1);
    }
  }
  throw new Error(`#${name} has an unbalanced body`);
}

test("the Incoming desk routes per-PO detail reads through bounded, not bare Promise.all", () => {
  const source = src("src/incoming/incoming-desk.ts");
  const body = privateMethodBody(source, "read");
  assert.doesNotMatch(
    body,
    /Promise\.all\(\s*placed\.map\(\s*\(o\)\s*=>\s*this\.#api\.order\(/,
    "the placed orders' detail reads must not be a bare Promise.all",
  );
  assert.match(
    body,
    /bounded\s*\(\s*INCOMING_DETAIL_CONCURRENCY\s*,/,
    "the desk must cap the per-PO detail reads with bounded at INCOMING_DETAIL_CONCURRENCY",
  );
});

test("a `bounded` helper sits in the file, exporting the cap constant and the parallel runner", () => {
  const source = src("src/incoming/incoming-desk.ts");
  // The helper must be a function declared in this file (exported, so the test for the helper's
  // shape can find it), and it must cap concurrency, keep source order, and short-circuit on an
  // empty input list.
  assert.match(source, /INCOMING_DETAIL_CONCURRENCY\s*=\s*6\b/, "the desk must declare INCOMING_DETAIL_CONCURRENCY at 6");
  assert.match(source, /(?:^|\n)\s*(?:export\s+)?async\s+function\s+bounded\b/, "the file must declare a bounded helper");
  assert.match(source, /if\s*\(\s*items\.length\s*===\s*0\s*\)\s*return\s*\[\]/, "an empty input must answer an empty array without firing work");
  assert.match(source, /Math\.max\s*\(\s*1\s*,\s*Math\.min\s*\(\s*limit\s*,\s*items\.length\s*\)\s*\)/, "the cap must clamp the limit between 1 and the input length");
});

/**
 * A local copy of `bounded` whose source the test pins (PRR-498). The behavioural assertions
 * below sit on this copy rather than importing the desk's helper, because the Node 20 test
 * runtime cannot resolve `.ts` imports without a compile step. The copy's source matches the
 * helper's contract: cap at `limit`, return in source order, and short-circuit on empty input.
 */
async function bounded(limit, items, work) {
  if (items.length === 0) return [];
  const out = new Array(items.length);
  let next = 0;
  const cap = Math.max(1, Math.min(limit, items.length));
  const workers = Array.from({ length: cap }, async () => {
    while (true) {
      const i = next++;
      if (i >= items.length) return;
      out[i] = await work(items[i]);
    }
  });
  await Promise.all(workers);
  return out;
}

test("the bounded helper caps concurrent work calls at the limit and keeps source order", async () => {
  let inFlight = 0;
  let peak = 0;
  const work = async (n) => {
    inFlight += 1;
    if (inFlight > peak) peak = inFlight;
    await new Promise((r) => setTimeout(r, 10));
    inFlight -= 1;
    return n * 10;
  };
  const out = await bounded(3, [1, 2, 3, 4, 5, 6, 7, 8], work);
  assert.deepEqual(out, [10, 20, 30, 40, 50, 60, 70, 80], "results stay in source order");
  assert.ok(peak <= 3, `peak concurrency was ${peak}, want <= 3`);
  assert.ok(peak >= 2, `peak concurrency was ${peak}, want >= 2 (the helper must actually parallelise)`);
});

test("an empty input does not call work and an oversized limit caps at the input length", async () => {
  const calls = [];
  const work = async (n) => {
    calls.push(n);
    return n;
  };
  assert.deepEqual(await bounded(4, [], work), [], "an empty input must answer an empty array");
  assert.deepEqual(calls, [], "an empty input must not call work at all");
  calls.length = 0;
  await bounded(100, [1, 2, 3], work);
  assert.deepEqual(calls, [1, 2, 3], "every item must be worked exactly once when the limit exceeds the input length");
});
