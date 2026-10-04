// The editors' floor warning (CP0-6 answer A), the pure half: what is asked of the
// server before a save, which of the products it names the editor must confirm, the
// words of the warning, and the body that logs the confirmation. The grading itself is
// the server's (the resolver and its margin gate); nothing here computes a margin.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  affectedItems,
  alreadyUnderCount,
  confirmationBody,
  itemLine,
  needsConfirm,
  previewBody,
  warningHeadline,
  warningNotes,
} from "../src/_pricing-floor.js";

const CAT = "ca700000-0000-4000-9000-000000000001";
const CUST = "c0000000-0000-4000-9000-000000000001";
const PROD = "a7000000-0000-4000-9000-000000000001";

const item = (over = {}) => ({
  productId: PROD, sku: "HPV-60", name: "Holland Stone 60 mm", customerType: "CONTRACTOR", finalCents: 300, marginBps: 800, floorBps: 2500,
  priceUom: "sf", previousFinalCents: 400, previousMarginBps: 3100, alreadyUnderFloor: false, ...over,
});
const preview = (items, over = {}) => ({ items, evaluated: 12, notGraded: 0, truncated: false, ...over });

const ruleWrite = { scope: "TIER_CATEGORY", customer_id: "", customer_type: "CONTRACTOR", category_id: CAT, product_id: "", branch_id: "", calc_type: "PCT_OFF_LIST", discount_bps: 4000, price_uom: "sf", priority: 1, detail_text: "", version: 0 };

test("each kind of change is asked in the shape the server reads", () => {
  assert.deepEqual(previewBody({ kind: "rule", id: "", write: ruleWrite }), { kind: "rule", rule: ruleWrite });
  assert.deepEqual(previewBody({ kind: "rule", id: "r1", write: ruleWrite }), { kind: "rule", id: "r1", rule: ruleWrite });
  assert.deepEqual(
    previewBody({ kind: "override", id: "o1", customerId: CUST, write: { category_id: CAT, calc_type: "PCT_OFF_LIST", discount_bps: 1, price_uom: "sf", notes: "", version: 1 } }),
    { kind: "override", id: "o1", customer_id: CUST, override: { category_id: CAT, calc_type: "PCT_OFF_LIST", discount_bps: 1, price_uom: "sf", notes: "", version: 1 } },
  );
  assert.deepEqual(
    previewBody({ kind: "category_policy", categoryId: CAT, write: { margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false } }),
    { kind: "category_policy", category_id: CAT, policy: { margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false } },
  );
  const brk = { code: "C", name: "N", category_id: CAT, product_id: "", threshold_uom: "PLT", items: [], version: 0 };
  assert.deepEqual(previewBody({ kind: "quantity_break", id: "", write: brk }), { kind: "quantity_break", quantity_break: brk });
});

test("a rule on one branch is graded against that branch's cost", () => {
  const body = previewBody({ kind: "rule", id: "", write: { ...ruleWrite, branch_id: "b0000000-0000-4000-9000-000000000001" } });
  assert.equal(body.branch_id, "b0000000-0000-4000-9000-000000000001");
});

test("the editor confirms what the new price makes worse, not what was already under", () => {
  const fresh = item();
  const already = item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400, previousFinalCents: 400 });
  const deeper = item({ productId: "p3", alreadyUnderFloor: true, finalCents: 250, previousFinalCents: 400 });
  const p = preview([fresh, already, deeper]);
  assert.deepEqual(affectedItems(p).map((i) => i.productId), [PROD, "p3"]);
  assert.equal(alreadyUnderCount(p), 1);
  assert.equal(needsConfirm(p), true);
  assert.equal(needsConfirm(preview([already])), false);
  assert.equal(needsConfirm(preview([])), false);
});

test("the headline says how many products, in the singular and the plural", () => {
  assert.equal(warningHeadline(preview([item()])), "This price puts 1 product under its margin floor.");
  assert.equal(warningHeadline(preview([item(), item({ productId: "p2" })])), "This price puts 2 products under their margin floor.");
});

test("a product's line gives the figures the server graded, in words", () => {
  assert.equal(
    itemLine(item()),
    "HPV-60, Holland Stone 60 mm: margin 8.00% against a floor of 25.00% for Contractor, price CA$3.00 per sf (was CA$4.00)",
  );
  assert.equal(itemLine(item({ customerType: "" })).includes("for "), false);
  // A margin policy change moves the floor, not the price: the line says the price is unchanged.
  assert.equal(
    itemLine(item({ customerType: "RETAIL", finalCents: 400, previousFinalCents: 400, marginBps: 3100, floorBps: 4000 })),
    "HPV-60, Holland Stone 60 mm: margin 31.00% against a floor of 40.00% for Retail, price CA$4.00 per sf, unchanged",
  );
  assert.match(itemLine(item({ customerType: "RETAIL" })), /for Retail/);
});

test("the notes say what the grading did not cover", () => {
  assert.deepEqual(warningNotes(preview([item()])), []);
  assert.deepEqual(warningNotes(preview([item()], { truncated: true, evaluated: 200 })), [
    "Only the first 200 products were checked; more are reached by this price.",
  ]);
  assert.deepEqual(warningNotes(preview([item()], { notGraded: 3 })), [
    "3 products could not be checked because they have no cost on file.",
  ]);
  assert.deepEqual(warningNotes(preview([item()], { notGraded: 1 })), ["1 product could not be checked because it has no cost on file."]);
  assert.deepEqual(warningNotes(preview([item(), item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400 })])), [
    "1 more product was already under its floor and is no worse.",
  ]);
});

test("the confirmation is logged against the record the save wrote, with the products shown", () => {
  const p = preview([item(), item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400 })]);
  assert.deepEqual(confirmationBody({ kind: "rule", id: "", write: ruleWrite }, "r-new", p), {
    entity_type: "pricing_rule", entity_id: "r-new", product_ids: [PROD], ref_entity_type: "category", ref_entity_id: CAT,
  });
  assert.deepEqual(
    confirmationBody({ kind: "override", id: "", customerId: CUST, write: { category_id: CAT, calc_type: "PCT_OFF_LIST", discount_bps: 1, price_uom: "sf", notes: "", version: 0 } }, "o-new", p),
    { entity_type: "customer_override", entity_id: "o-new", product_ids: [PROD], ref_entity_type: "customer", ref_entity_id: CUST },
  );
  assert.deepEqual(
    confirmationBody({ kind: "category_policy", categoryId: CAT, write: { margin_target_bps: 3000, margin_floor_bps: 2000, margin_enforcement_disabled: false } }, CAT, p),
    { entity_type: "category_policy", entity_id: CAT, product_ids: [PROD], ref_entity_type: "category", ref_entity_id: CAT },
  );
  const productRule = { ...ruleWrite, scope: "CUSTOMER_PRODUCT", customer_id: CUST, product_id: PROD, category_id: "" };
  assert.deepEqual(confirmationBody({ kind: "rule", id: "r1", write: productRule }, "r1", p).ref_entity_type, "product");
  const brk = { code: "C", name: "N", category_id: "", product_id: PROD, threshold_uom: "PLT", items: [], version: 0 };
  const b = confirmationBody({ kind: "quantity_break", id: "", write: brk }, "b-new", p);
  assert.equal(b.entity_type, "quantity_break");
  assert.equal(b.ref_entity_type, "product");
});

test("a tier rule with no category has no reference to link, and the body leaves the pair out", () => {
  const tierDefault = { ...ruleWrite, scope: "TIER_DEFAULT", category_id: "" };
  const body = confirmationBody({ kind: "rule", id: "", write: tierDefault }, "r-new", preview([item()]));
  assert.equal("ref_entity_type" in body, false);
  assert.equal("ref_entity_id" in body, false);
});
