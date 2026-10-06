// The compare table's Basis words (visual review round 1, P3): the ERP words
// the pricing source for people (pricing-words-logic.ts priceSourceWord), and
// the app showed the raw enum. The raw comparisonBasis stays as the wire
// reading; comparisonBasisWord is the one the table prints.
import { test } from "node:test";
import assert from "node:assert/strict";
import { comparisonBasis, comparisonBasisWord, priceSourceWord } from "../src/_quote-edit.js";

test("the ERP's own words for every pricing source it names", () => {
  assert.equal(priceSourceWord("CUSTOMER_PRODUCT_RULE"), "Customer + product");
  assert.equal(priceSourceWord("CUSTOMER_CATEGORY_RULE"), "Customer + category");
  assert.equal(priceSourceWord("CUSTOMER_OVERRIDE"), "Customer override");
  assert.equal(priceSourceWord("QUANTITY_BREAK"), "Quantity break");
  assert.equal(priceSourceWord("TIER_CATEGORY"), "Tier + category");
  assert.equal(priceSourceWord("TIER_DEFAULT"), "Tier default");
  assert.equal(priceSourceWord("PROMOTION"), "Promotion");
  assert.equal(priceSourceWord("LIST"), "List price");
});

test("an unmapped source degrades to plain lower case words, never an enum", () => {
  assert.equal(priceSourceWord("CUSTOMER_RULE"), "customer rule");
});

test("the basis word keeps the floor block and its policy as sent, and leaves the raw reading alone", () => {
  assert.equal(comparisonBasisWord({ blocked: false, source: "TIER_CATEGORY" }), "Tier + category");
  assert.equal(comparisonBasisWord({ blocked: true, source: "X", block_policy: "category_margin_floor" }), "below floor (category_margin_floor)");
  assert.equal(comparisonBasisWord({ blocked: true, source: "X" }), "below floor");
  assert.equal(comparisonBasis({ blocked: false, source: "TIER_CATEGORY" }), "TIER_CATEGORY");
});
