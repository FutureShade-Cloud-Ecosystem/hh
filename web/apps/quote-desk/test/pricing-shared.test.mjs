// The pricing panel's shared words and number handling, unit tested on the Node
// runner. Money and percentages move between the screen and the wire by string
// handling only: 1.005 * 100 is 100.49999999999999, so a typed amount is never
// multiplied.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  categoryLabel,
  centsText,
  dollarsToCents,
  entityLabel,
  formatMoney,
  formatPct,
  auditActionWord,
  auditFieldWord,
  bpsText,
  isKnownCategory,
  pctToBps,
  scopeLabel,
  CATEGORY_OPTIONS,
} from "../src/_pricing-shared.js";

test("pctToBps reads a typed percentage by its digits, never by multiplying", () => {
  assert.equal(pctToBps("18"), 1800);
  assert.equal(pctToBps("18.5"), 1850);
  assert.equal(pctToBps(" 18.50 "), 1850);
  assert.equal(pctToBps("0.07"), 7);
  assert.equal(pctToBps(".5"), 50);
  assert.equal(pctToBps("1.005"), null, "a third decimal is refused, not rounded");
  assert.equal(pctToBps("4.35"), 435);
  assert.equal(pctToBps(""), null);
  assert.equal(pctToBps("abc"), null);
  assert.equal(pctToBps("-5"), null);
  assert.equal(pctToBps("1e2"), null);
});

test("dollarsToCents reads a typed amount by its digits", () => {
  assert.equal(dollarsToCents("12.34"), 1234);
  assert.equal(dollarsToCents("1.005"), null);
  assert.equal(dollarsToCents("0.29"), 29);
  assert.equal(dollarsToCents("19.99"), 1999);
  assert.equal(dollarsToCents("8"), 800);
  assert.equal(dollarsToCents(""), null);
  assert.equal(dollarsToCents("$5"), null);
});

test("bpsText and centsText fill an edit box from the wire's integers", () => {
  assert.equal(bpsText(3400), "34.00");
  assert.equal(bpsText(7), "0.07");
  assert.equal(bpsText(0), "0.00");
  assert.equal(centsText(1999), "19.99");
  assert.equal(centsText(5), "0.05");
});

test("formatPct and formatMoney render integers as words", () => {
  assert.equal(formatPct(3000), "30.00%");
  assert.equal(formatPct(1), "0.01%");
  assert.equal(formatMoney(123456), "$1,234.56");
  assert.equal(formatMoney(5), "$0.05");
  assert.equal(formatMoney(-250), "-$2.50");
});

test("categories are named for the six seeded ids and degrade to a short id otherwise", () => {
  assert.equal(categoryLabel("ca700000-0000-4000-9000-000000000001"), "Pavers");
  assert.equal(categoryLabel("ca700000-0000-4000-9000-000000000006"), "Bulk");
  assert.equal(categoryLabel("deadbeef-0000-4000-9000-000000000001"), "deadbeef");
  assert.equal(isKnownCategory("ca700000-0000-4000-9000-000000000002"), true);
  assert.equal(isKnownCategory("deadbeef-0000-4000-9000-000000000001"), false);
  assert.equal(CATEGORY_OPTIONS.length, 6);
});

test("scopes, entities, actions and fields are said in words, and an unknown one passes through", () => {
  assert.equal(scopeLabel("TIER_CATEGORY"), "Tier + category");
  assert.equal(scopeLabel("CUSTOMER_PRODUCT"), "Customer + product");
  assert.equal(scopeLabel("SOMETHING_NEW"), "SOMETHING_NEW");
  assert.equal(entityLabel("category_policy"), "Category policy");
  assert.equal(entityLabel("quantity_break"), "Quantity break");
  assert.equal(entityLabel("unknown_thing"), "unknown_thing");
  assert.equal(auditActionWord("CREATE"), "Created");
  assert.equal(auditActionWord("BYPASS"), "Bypassed");
  assert.equal(auditActionWord("NEW_VERB"), "new verb");
  assert.equal(auditFieldWord("floor_warning_confirmed"), "Floor warning confirmed");
  assert.equal(auditFieldWord("margin_target_bps"), "margin_target_bps");
  assert.equal(auditFieldWord(undefined), "");
});
