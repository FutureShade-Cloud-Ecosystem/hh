// The slice S8 Expected column floor, board-driven: the Receiving queue's
// Expected column carries relative day words ("in 2 days", "in 4 days")
// that the board says must never truncate to "in 2 da..." (READ-BS5 board
// fault under bs-receiving-an-order-to-receive, ruling R5). The CSS
// holds the column to a nowrap floor of 6.5rem so the table scrolls
// inside its card rather than crushing a cell.
//
// Red first: against the pre-S8 source these assertions would fail (the
// HTML had no `data-expected-col` on the th or td and the styles file had
// no min-width / nowrap rule for the Expected column).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { functionBody } from "./_slice.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const appDir = join(here, "..");
const src = (rel) => readFileSync(join(appDir, rel), "utf8");

test("the Receiving queue marks Expected with a data-expected-col on th and td", () => {
  const source = src("src/receiving/receiving-views.ts");
  // The queue is the function that draws it; taking its body (and proving it is the queue) means a moved or
  // renamed section comment cannot widen or empty the text the assertions read.
  const queueRange = functionBody(source, "queue");
  assert.ok(
    queueRange.includes('aria-label="Orders to receive"'),
    "the function named queue must be the one that draws the Orders to receive panel",
  );
  assert.ok(
    /<th data-expected-col>Expected<\/th>/.test(queueRange),
    "the queue's Expected header must carry data-expected-col",
  );
  assert.ok(
    /<td data-expected-col data-label="Expected">/.test(queueRange),
    "the queue's Expected cell must carry data-expected-col",
  );
});

test("the purchasing stylesheet floors the Expected column to a nowrap 6.5rem", () => {
  const css = src("src/purch/purch-styles.ts");
  assert.ok(
    /\[data-queue\]\s+th\[data-expected-col\][\s\S]*?\[data-queue\]\s+td\[data-expected-col\]\s*\{\s*white-space:\s*nowrap;\s*min-width:\s*6\.5rem;\s*\}/.test(css),
    "purch-styles.ts must declare a [data-queue] th/td[data-expected-col] rule with white-space: nowrap and min-width: 6.5rem",
  );
});
