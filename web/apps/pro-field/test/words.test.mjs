// The words a contractor reads (UX-37, glossary): an unknown figure says what is missing, one word for
// the day asked for, "your dealer" never dealer internals. Pure functions and the order row's words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { priceWords, qtyWords } from "../src/figures.ts";
import { orderRowWords } from "../src/views.ts";
import { promisedWords } from "../src/timeline.ts";

test("an unknown price reads Not priced and an unknown quantity Not counted, never n/a", () => {
  assert.equal(priceWords(undefined), "Not priced");
  assert.equal(priceWords(null), "Not priced");
  assert.equal(priceWords(Number.NaN), "Not priced");
  assert.equal(priceWords(123400), "$1,234.00");
  assert.equal(priceWords(0), "$0.00");
  assert.equal(qtyWords(undefined, "t"), "Not counted");
  assert.equal(qtyWords("", "t"), "Not counted");
  assert.equal(qtyWords("12.5", "cu yd"), "12.5 cu yd");
});

test("an order with no asked-for day says Not set in its row and its promise, never a blank or n/a", () => {
  const row = orderRowWords({ order_no: "SO-1", status: "CONFIRMED", fulfillment: "PICKUP", total_cents: 1000, requested_date: "" });
  assert.ok(row.includes("Not set"), row);
  assert.doesNotMatch(row, /n\/a/);
  assert.equal(promisedWords({ fulfillment: "PICKUP", requested_date: "" }).day, "Not set");
});
