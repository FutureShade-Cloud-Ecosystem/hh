// HH-419: the floor preview serves the margin legs only to a caller on the cost list. For anyone
// else they are absent on the wire, and the reading keeps them null, never a zero that would render
// as a break-even product.
import { test } from "node:test";
import assert from "node:assert/strict";
import { toFloorPreview } from "../src/pricing-api.ts";

const bare = {
  product_id: "p1", sku: "HPV-60", name: "Charcoal", final_cents: 300, price_uom: "sf", source: "TIER_CATEGORY",
  previous_final_cents: 400, already_under_floor: false,
};

test("an item without the margin legs reads them as null, not zero", () => {
  const p = toFloorPreview({ items: [bare], evaluated: 1, not_graded: 0, no_cost: 0, truncated: false });
  assert.equal(p.items[0].margin_bps, null);
  assert.equal(p.items[0].floor_bps, null);
  assert.equal(p.items[0].previous_margin_bps, null);
  assert.equal(p.items[0].final_cents, 300);
});

test("an item with the margin legs keeps them, a zero margin included", () => {
  const p = toFloorPreview({ items: [{ ...bare, margin_bps: 0, floor_bps: 2500, previous_margin_bps: 3100 }], evaluated: 1 });
  assert.equal(p.items[0].margin_bps, 0);
  assert.equal(p.items[0].floor_bps, 2500);
  assert.equal(p.items[0].previous_margin_bps, 3100);
});
