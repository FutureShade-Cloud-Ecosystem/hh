// The slice S8 vocabulary rule, board-driven: the Inventory Stock lookup
// panel shows On hand, Reserved (not Allocated), Available and the
// footnote that says "Available is on hand minus reserved". The browse and
// branch-stock panes already say Reserved; only the lookup used Allocated
// (READ-BS5 finding 6, ruling R6). The testers' claim under public/ is
// held to the same word.
//
// Red first: against the pre-S8 source these assertions would fail
// (lookupPane said "Allocated", the footnote said "minus allocated", and
// the testers' claim named "allocated").
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const appDir = join(here, "..");
const src = (rel) => readFileSync(join(appDir, rel), "utf8");

test("the stock lookup panel reserves the word (not Allocated) in its table header and cell", () => {
  const source = src("src/inventory/inventory-views.ts");
  const lookupStart = source.indexOf("function lookupPane(");
  const lookupEnd = source.indexOf("// ---- browse");
  const lookupRange = source.slice(lookupStart, lookupEnd);
  assert.ok(
    /<th class="num">Reserved<\/th>/.test(lookupRange),
    "the lookup table header must read Reserved, not Allocated",
  );
  assert.ok(
    /<td class="num" data-label="Reserved">/.test(lookupRange),
    "the lookup table cell must carry data-label=\"Reserved\"",
  );
  assert.ok(
    !/<th class="num">Allocated<\/th>/.test(lookupRange),
    "the lookup table header must no longer read Allocated",
  );
  assert.ok(
    !/data-label="Allocated"/.test(lookupRange),
    "no lookup table cell may carry data-label=\"Allocated\"",
  );
});

test("the stock lookup footnote names the same vocabulary: on hand minus reserved", () => {
  const source = src("src/inventory/inventory-views.ts");
  const lookupStart = source.indexOf("function lookupPane(");
  const lookupEnd = source.indexOf("// ---- browse");
  const lookupRange = source.slice(lookupStart, lookupEnd);
  assert.ok(
    /Available is on hand minus reserved\. A negative available is stock sold before its receipt, by design\./.test(lookupRange),
    "the footnote must read 'on hand minus reserved'",
  );
  assert.ok(
    !/on hand minus allocated/.test(lookupRange),
    "the footnote must no longer read 'minus allocated'",
  );
});

test("the testers' inv-lookup claim uses the same vocabulary: reserved, not allocated", () => {
  const claimSource = src("public/testers.html");
  const claim = claimSource.match(/<p data-claim="inv-lookup">[\s\S]*?<\/p>/)?.[0] ?? "";
  assert.ok(
    /\(on hand, reserved, available\)/.test(claim),
    "the inv-lookup claim must name (on hand, reserved, available)",
  );
  assert.ok(
    !/\(on hand, allocated, available\)/.test(claim) && !/\ballocated\b/.test(claim),
    "the inv-lookup claim must no longer name the word 'allocated'",
  );
});
