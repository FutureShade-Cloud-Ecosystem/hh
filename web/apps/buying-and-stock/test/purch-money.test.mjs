// The purchasing slices' money and quantity text: what a person types becomes integer cents or a
// decimal quantity through the decimal string, never a float multiply; anything else is refused in
// words before a request is sent.
import { test } from "node:test";
import assert from "node:assert/strict";
import { centsToField, parseAmountToCents, parseQty, parseRate } from "../src/purch/purch-money.ts";

test("an amount is read through the decimal string, so the half cent cases land exactly", () => {
  assert.equal(parseAmountToCents("19.99"), 1999);
  assert.equal(parseAmountToCents("0.1"), 10);
  assert.equal(parseAmountToCents("1.005"), null);
  assert.equal(parseAmountToCents("$1,250.50"), 125050);
  assert.equal(parseAmountToCents("1250"), 125000);
  assert.equal(parseAmountToCents("0"), 0);
});

test("an amount that is signed, loose, doubled or empty is refused, not guessed", () => {
  for (const bad of ["", "  ", "-5", "+5", "1,25", "12,34,56", "1e3", "abc", "1.2.3", "1234567890", "$"]) {
    assert.equal(parseAmountToCents(bad), null, `"${bad}" must be refused`);
  }
});

test("cents go back into an amount box as dollars and cents", () => {
  assert.equal(centsToField(125050), "1250.50");
  assert.equal(centsToField(5), "0.05");
});

test("a quantity is a positive decimal of at most four places", () => {
  assert.deepEqual(parseQty("12"), { ok: true, value: 12 });
  assert.deepEqual(parseQty("0.25"), { ok: true, value: 0.25 });
  assert.deepEqual(parseQty(" 3.1416 "), { ok: true, value: 3.1416 });
  for (const bad of ["", "0", "0.0", "-1", "1.00001", "1,000", "ten", "1e3"]) {
    assert.equal(parseQty(bad).ok, false, `"${bad}" must be refused`);
  }
});

test("a quantity that may be zero reads zero as a figure (a count of what is left)", () => {
  assert.deepEqual(parseQty("0", { allowZero: true }), { ok: true, value: 0 });
  assert.equal(parseQty("-1", { allowZero: true }).ok, false);
});

test("an exchange rate is a positive decimal of at most six places", () => {
  assert.deepEqual(parseRate("1.352"), { ok: true, value: 1.352 });
  assert.deepEqual(parseRate("0.000001"), { ok: true, value: 0.000001 });
  for (const bad of ["", "0", "-1.2", "1.2345678", "x", "1,2"]) {
    assert.equal(parseRate(bad).ok, false, `"${bad}" must be refused`);
  }
});
