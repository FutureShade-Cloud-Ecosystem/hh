// Quantities are exact decimals, never binary float sums (round 10 review): 0.1 + 0.2 is 0.3.
import assert from "node:assert/strict";
import test from "node:test";
import { addDecimal } from "../src/_quote-edit.ts";

test("two quantities add as exact decimals at the finer scale", () => {
  assert.equal(addDecimal("0.1", 0.2), "0.3");
  assert.equal(addDecimal("2", 2), "4");
  assert.equal(addDecimal("1.25", 1), "2.25");
  assert.equal(addDecimal("0.10", 0.2), "0.30");
  assert.equal(addDecimal("120", 0.005), "120.005");
  assert.equal(addDecimal("1000000.1", 0.2), "1000000.3");
});

test("a quantity that is not a plain decimal adds to nothing", () => {
  assert.equal(addDecimal("abc", 1), null);
  assert.equal(addDecimal("1", Number.NaN), null);
  assert.equal(addDecimal("1", 1e-7), null);
  assert.equal(addDecimal("-1", 1), null);
});
