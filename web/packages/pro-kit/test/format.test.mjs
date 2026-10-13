// Money, tax, quantity and day words. US dollars; tax exactly as the core
// returns it (never recomputed here); an unknown figure reads as a placeholder,
// never as a confident zero; ids and totals ride the mono face.
import { test } from "node:test";
import assert from "node:assert/strict";
import { NO_FIGURE, money, totalsLine, qtyWithUnit, houseDay, monoStyles } from "../src/format.ts";

test("money reads as US dollars with grouping and no currency letters", () => {
  assert.equal(money(123400), "$1,234.00");
  assert.equal(money(0), "$0.00");
  assert.equal(money(5), "$0.05");
  assert.equal(money(-123400), "-$1,234.00");
  assert.doesNotMatch(money(104864000), /[A-Za-z]/);
});

test("an absent or unusable figure is the placeholder, not a zero", () => {
  for (const value of [null, undefined, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(money(value), NO_FIGURE);
  }
  assert.notEqual(NO_FIGURE, "$0.00");
});

test("the totals line shows the core's own tax, even when it is not the rate times the subtotal", () => {
  // 7.75 percent of 10000 is 775; the core said 800 (a tax exempt line mix, for instance). It is shown as returned.
  const line = totalsLine({ subtotal_cents: 10000, tax_cents: 800, total_cents: 10800 });
  assert.deepEqual(line, { subtotal: "$100.00", tax: "$8.00", total: "$108.00" });
});

test("the totals line leaves a missing tax as the placeholder", () => {
  assert.deepEqual(totalsLine({ subtotal_cents: 10000 }), { subtotal: "$100.00", tax: NO_FIGURE, total: NO_FIGURE });
});

test("a quantity keeps the core's decimal string and appends its unit", () => {
  assert.equal(qtyWithUnit("12.5", "SF"), "12.5 SF");
  assert.equal(qtyWithUnit("3", ""), "3");
  assert.equal(qtyWithUnit(null, "EA"), NO_FIGURE);
});

test("a day reads in the dealer's own zone, not UTC", () => {
  // 2000-03-02 03:30 UTC is still the evening of March 1 in San Diego.
  assert.equal(houseDay("2000-03-02T03:30:00Z"), "Mar 1");
  assert.equal(houseDay("not a date"), NO_FIGURE);
  assert.equal(houseDay(null), NO_FIGURE);
});

test("the mono sheet names the mono token for ids and totals", () => {
  assert.match(String(monoStyles), /--hh-font-mono/);
});
