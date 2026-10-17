// The terms answer is read field by field (a missing required field refuses
// it), and a typed delivery fee is dollars and cents or it is refused.
import assert from "node:assert/strict";
import { test } from "node:test";
import { validateTerms } from "../src/qd-client.ts";
import { parseDeliveryFee } from "../src/_quote-workarea.ts";

const good = {
  quote_id: "q1",
  deposit_required: false,
  deposit_pct: 0,
  include_images: true,
  fulfillment: "PICKUP",
  delivery_fee_cents: 0,
  deposit_cents: 0,
  subtotal_cents: 100,
  tax_cents: 13,
  total_cents: 113,
  tax_label: "HST",
  lines: [],
};

test("the terms carry the dealer's tax word, and an answer without it is refused, never guessed", () => {
  assert.equal(validateTerms(good)?.taxLabel, "HST");
  assert.equal(validateTerms({ ...good, tax_label: "Sales tax" })?.taxLabel, "Sales tax");
  const { tax_label: _gone, ...bare } = good;
  assert.equal(validateTerms(bare), null);
  assert.equal(validateTerms({ ...good, tax_label: "" }), null);
});

test("a complete terms answer reads", () => {
  assert.equal(validateTerms(good)?.deliveryFeeCents, 0);
  assert.equal(validateTerms({ ...good, fulfillment: "DELIVERY", delivery_fee_cents: 2500 })?.fulfillment, "DELIVERY");
});

test("a missing delivery fee, deposit figure or fulfillment, or an unknown fulfillment, refuses the answer", () => {
  for (const key of ["delivery_fee_cents", "deposit_cents", "fulfillment"]) {
    const { [key]: _gone, ...rest } = good;
    assert.equal(validateTerms(rest), null, key);
  }
  assert.equal(validateTerms({ ...good, fulfillment: "TELEPORT" }), null);
});

test("a delivery fee is plain dollars and cents; anything else is refused, never zero", () => {
  assert.deepEqual(parseDeliveryFee(""), { cents: 0 });
  assert.deepEqual(parseDeliveryFee("25"), { cents: 2500 });
  assert.deepEqual(parseDeliveryFee("25.5"), { cents: 2550 });
  assert.deepEqual(parseDeliveryFee(" 25.05 "), { cents: 2505 });
  for (const bad of ["$25", "1,250", "25.123", "-5", "abc", "1e3"]) {
    assert.ok("error" in parseDeliveryFee(bad), bad);
  }
});
