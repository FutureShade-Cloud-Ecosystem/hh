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
  settledNote,
  previewBody,
  warningHeadline,
  warningNotes,
} from "../src/_pricing-floor.js";

const CAT = "ca700000-0000-4000-9000-000000000001";
const CUST = "c0000000-0000-4000-9000-000000000001";
const PROD = "a7000000-0000-4000-9000-000000000001";

const item = (over = {}) => ({
  productId: PROD, sku: "HPV-60", name: "Holland Stone 60 mm", customerType: "CONTRACTOR", branchId: "", branchCode: "", branchName: "",
  finalCents: 300, marginBps: 800, floorBps: 2500, priceUom: "sf", previousFinalCents: 400, previousMarginBps: 3100,
  alreadyUnderFloor: false, ...over,
});
const preview = (items, over = {}) => ({
  items, evaluated: 12, notGraded: 0, noCost: 0, truncated: false, noCostBranches: [], notGradedBranches: 0, ...over,
});

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
  assert.equal(needsConfirm(preview([])), false);
  // The reviewer's P2-B shape: the server answers ONE item, an already-under combination whose
  // price the change dropped (a 90 percent break undercutting the RETAIL tier alone). That one
  // item alone must open the warning, never read as "no worse" and saved past with no log row.
  const undercut = item({ customerType: "RETAIL", alreadyUnderFloor: true, finalCents: 1000, previousFinalCents: 3000, marginBps: -6000 });
  const single = preview([undercut]);
  assert.equal(needsConfirm(single), true);
  assert.deepEqual(affectedItems(single).map((i) => i.customerType), ["RETAIL"]);
  assert.match(itemLine(undercut), /\(was CA\$30\.00\)/);
});

test("the editor is never saved past a check that could not grade, nor past a count it could not cover", () => {
  // the three answers a silent save used to follow
  assert.equal(needsConfirm(preview([], { notGraded: 9 })), true, "nothing could be priced");
  assert.equal(needsConfirm(preview([], { truncated: true })), true, "the check stopped at its limit");
  const already = item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400, previousFinalCents: 400 });
  assert.equal(needsConfirm(preview([already])), false, "products already under the floor and no worse are not newly at risk: said after the save, not a stop");
  assert.equal(needsConfirm(preview([], { noCost: 2 })), true, "products with no cost on file could not be graded");
  assert.equal(needsConfirm(preview([], { evaluated: 4 })), false, "a clean, complete answer saves straight away");
});

test("a notice with no product newly under the floor says what could not be checked, neutrally", () => {
  assert.equal(warningHeadline(preview([], { notGraded: 9 })), "The floor check could not cover everything this price reaches.");
  assert.equal(warningHeadline(preview([], { truncated: true })), "The floor check could not cover everything this price reaches.");
});

test("a save past products that were already under the floor and are no worse says so afterwards, in words", () => {
  const already = item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400, previousFinalCents: 400 });
  assert.equal(settledNote(preview([already])), "Saved. 1 product was already under its margin floor and is no worse.");
  assert.equal(settledNote(preview([already, { ...already, productId: "p3" }])), "Saved. 2 products were already under their margin floor and are no worse.");
  assert.equal(settledNote(preview([])), "");
  assert.equal(settledNote(preview([item()])), "", "a warned save is told in the warning");
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

test("a line names the branch the worst grading happened at when the server names one", () => {
  // A change that named no branch is graded at every branch, worst kept: the editor must see
  // which yard breaches, or a breach at one yard reads like every yard's problem. The code
  // keeps the line short (the seeded names run long); the full name rides in the item's title.
  assert.equal(
    itemLine(item({ branchId: "b0000000-0000-4000-9000-000000000002", branchCode: "KGN", branchName: "Dibbits Landscape Supply, Kingston" })),
    "HPV-60, Holland Stone 60 mm: margin 8.00% against a floor of 25.00% for Contractor at KGN, price CA$3.00 per sf (was CA$4.00)",
  );
  // No code from the server: the name still says the yard.
  assert.equal(
    itemLine(item({ branchId: "b0000000-0000-4000-9000-000000000002", branchName: "Kingston" })),
    "HPV-60, Holland Stone 60 mm: margin 8.00% against a floor of 25.00% for Contractor at Kingston, price CA$3.00 per sf (was CA$4.00)",
  );
  // A change that named its branch already told the editor which yard: no name, no suffix.
  assert.equal(itemLine(item({ branchId: "b0000000-0000-4000-9000-000000000002" })).includes(" at "), false);
});

test("the notes say what the grading did not cover", () => {
  assert.deepEqual(warningNotes(preview([item()])), []);
  assert.deepEqual(warningNotes(preview([item()], { truncated: true, evaluated: 200 })), [
    "The check stopped at its limit after 200 products; the rest could not be checked.",
  ]);
  assert.deepEqual(warningNotes(preview([item()], { truncated: true, evaluated: 40, notGradedBranches: 1 })), [
    "The check stopped at its limit after 40 products; the rest could not be checked. 1 branch was not graded.",
  ]);
  assert.deepEqual(warningNotes(preview([item()], { truncated: true, evaluated: 2, notGradedBranches: 3 })), [
    "The check stopped at its limit after 2 products; the rest could not be checked. 3 branches were not graded.",
  ]);
  assert.deepEqual(warningNotes(preview([item()], { notGraded: 3 })), ["3 products could not be priced, so they could not be checked."]);
  assert.deepEqual(warningNotes(preview([item()], { notGraded: 1 })), ["1 product could not be priced, so it could not be checked."]);
  assert.deepEqual(warningNotes(preview([item()], { noCost: 2 })), ["2 products have no cost on file, so their margin could not be told."]);
  assert.deepEqual(warningNotes(preview([item()], { noCost: 1 })), ["1 product has no cost on file, so its margin could not be told."]);
  assert.deepEqual(warningNotes(preview([item(), item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400 })])), [
    "1 more product was already under its floor and is no worse.",
  ]);
});

// The reviewer's round 4 note: a branch-less check skips a yard with no cost row and grades
// the branches that carry one; the notice says which yard and for how many products, without
// ever holding the save back (needsConfirm stays closed by it).
test("a branch with no cost on file is said with its name and count, and never holds the save back", () => {
  const noCostAt = { branchId: "b0000000-0000-4000-9000-000000000002", branchCode: "KGN", branchName: "Kingston", products: 9 };
  assert.deepEqual(warningNotes(preview([], { noCostBranches: [noCostAt] })), [
    "No cost on file at Kingston for 9 products, so that yard was not graded.",
  ]);
  assert.deepEqual(warningNotes(preview([], { noCostBranches: [{ ...noCostAt, products: 1 }] })), [
    "No cost on file at Kingston for 1 product, so that yard was not graded.",
  ]);
  // A yard the answer could not name falls back to its id.
  assert.deepEqual(warningNotes(preview([], { noCostBranches: [{ ...noCostAt, branchName: "" }] })), [
    `No cost on file at ${noCostAt.branchId} for 9 products, so that yard was not graded.`,
  ]);
  assert.equal(needsConfirm(preview([], { noCostBranches: [noCostAt] })), false, "the graded branches carry the check: this is said, not a stop");
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

test("a notice that names no product has nothing to log", () => {
  const rule = { kind: "rule", id: "", write: ruleWrite };
  assert.equal(confirmationBody(rule, "r-new", preview([], { notGraded: 9 })), null);
  const already = item({ productId: "p2", alreadyUnderFloor: true, finalCents: 400, previousFinalCents: 400 });
  assert.equal(confirmationBody(rule, "r-new", preview([already], { noCost: 1 })), null, "already under products are not what the editor was warned of");
});
