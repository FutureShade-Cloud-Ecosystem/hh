// The pricing panel's wire readers: each proves every field it needs or answers
// null, and the client turns a null into a refusal in words. A missing field is a
// refusal, never a default, and one unreadable row refuses the whole page rather
// than showing it short.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  readAuditPage,
  readBreak,
  readBreakPage,
  readCreatedRef,
  readFloorPreview,
  readOverridePage,
  readPolicy,
  readPolicyPage,
  readRule,
  readRulePage,
} from "../src/_pricing-wire.js";

const CAT = "ca700000-0000-4000-9000-000000000001";

const rule = {
  id: "r1", scope: "TIER_CATEGORY", customer_type: "CONTRACTOR", category_id: CAT, calc_type: "PCT_OFF_LIST",
  discount_bps: 1800, price_uom: "SF", priority: 40, detail_text: "Contractor, Pavers", is_active: true, version: 3,
};
const override = { id: "o1", customer_id: "c1", category_id: CAT, calc_type: "PCT_OFF_LIST", discount_bps: 2200, price_uom: "SF", notes: "n", version: 1 };
const brk = {
  id: "b1", code: "PAVER-FULL-SKID", name: "Full skid", category_id: CAT, threshold_uom: "PLT", is_active: true, version: 2,
  items: [{ from_qty: 1, to_qty: null, value_type: "PCT_OFF", discount_bps: 500, price_uom: "SF" }],
};
const audit = { id: "a1", entity_type: "category_policy", entity_id: CAT, action: "UPDATE", field_name: "margin_target_bps", old_value: "3300", new_value: "3400", user_id: "u1", created_at: "2026-10-04T00:21:00Z", ref_entity_type: "category", ref_entity_id: CAT };

test("a category policy page reads, and one bad row refuses the page", () => {
  const page = readPolicyPage({ items: [{ category_id: CAT, margin_target_bps: 3400, margin_floor_bps: 2000, margin_enforcement_disabled: false }], next_cursor: null });
  assert.deepEqual(page, { items: [{ categoryId: CAT, targetBps: 3400, floorBps: 2000, enforcementOff: false }], nextCursor: null });
  assert.equal(readPolicyPage({ items: [{ category_id: CAT, margin_target_bps: 3400, margin_floor_bps: 2000 }] }), null, "enforcement flag missing");
  assert.equal(readPolicyPage({ items: [{ category_id: CAT, margin_target_bps: "34", margin_floor_bps: 2000, margin_enforcement_disabled: false }] }), null);
  assert.equal(readPolicyPage({}), null);
  assert.equal(readPolicy({ category_id: CAT, margin_target_bps: 1, margin_floor_bps: 0, margin_enforcement_disabled: true })?.enforcementOff, true);
});

test("a rule reads with its calc value, and refuses a missing version or an unknown calc type", () => {
  const r = readRule(rule);
  assert.equal(r?.id, "r1");
  assert.equal(r?.discountBps, 1800);
  assert.equal(r?.priceCents, null);
  assert.equal(r?.customerType, "CONTRACTOR");
  assert.equal(r?.validFrom, null);
  assert.equal(readRule({ ...rule, version: undefined }), null);
  assert.equal(readRule({ ...rule, calc_type: "HALF_OFF" }), null);
  assert.equal(readRule({ ...rule, scope: "NOWHERE" }), null);
  assert.equal(readRule({ ...rule, is_active: "yes" }), null);
  assert.equal(readRulePage({ items: [rule, { ...rule, id: undefined }] }), null);
  assert.equal(readRulePage({ items: [rule], next_cursor: "abc" })?.nextCursor, "abc");
});

test("a rule's valid dates are read as the wire sends them, null when open", () => {
  assert.equal(readRule({ ...rule, valid_from: "2026-01-01T00:00:00Z" })?.validFrom, "2026-01-01T00:00:00Z");
  assert.equal(readRule({ ...rule, valid_to: null })?.validTo, null);
});

test("an override page reads and refuses a calc type it cannot show", () => {
  const page = readOverridePage({ items: [override] });
  assert.equal(page?.items[0]?.discountBps, 2200);
  assert.equal(page?.items[0]?.notes, "n");
  assert.equal(readOverridePage({ items: [{ ...override, calc_type: "ABSOLUTE" }] }), null);
});

test("a quantity break reads with its tiers, open ended tiers null, surcharge tiers kept", () => {
  const b = readBreak(brk);
  assert.equal(b?.items.length, 1);
  assert.equal(b?.items[0]?.toQty, null);
  assert.equal(b?.items[0]?.discountBps, 500);
  const withSur = readBreak({ ...brk, items: [{ from_qty: 0, to_qty: 2000, value_type: "SURCHARGE_PCT", surcharge_bps: 1000, price_uom: "KG" }] });
  assert.equal(withSur?.items[0]?.valueType, "SURCHARGE_PCT");
  assert.equal(withSur?.items[0]?.surchargeBps, 1000);
  assert.equal(readBreak({ ...brk, items: [{ from_qty: 1, value_type: "WHATEVER", price_uom: "SF" }] }), null);
  assert.equal(readBreak({ ...brk, items: undefined }), null);
  assert.equal(readBreak({ ...brk, threshold_uom: "" }), null);
  assert.equal(readBreakPage({ items: [brk] })?.items[0]?.code, "PAVER-FULL-SKID");
});

test("an audit row reads with its drill through pair and keeps unlinked rows unlinked", () => {
  const page = readAuditPage({ items: [audit, { ...audit, id: "a2", ref_entity_type: undefined, ref_entity_id: undefined }], next_cursor: null });
  assert.equal(page?.items[0]?.refEntityType, "category");
  assert.equal(page?.items[1]?.refEntityType, undefined);
  assert.equal(readAuditPage({ items: [{ ...audit, created_at: undefined }] }), null);
  assert.equal(readAuditPage({ items: [{ ...audit, action: "SMASH" }] }), null);
});

test("a created reference needs its id and version", () => {
  assert.deepEqual(readCreatedRef({ id: "x", version: 1 }), { id: "x", version: 1 });
  assert.equal(readCreatedRef({ id: "x" }), null);
});

test("a floor preview reads its items and counts, and refuses a half answer", () => {
  const good = {
    items: [{ product_id: "p1", sku: "HPV-60", name: "Holland", final_cents: 300, margin_bps: 800, floor_bps: 2500, price_uom: "sf", source: "TIER_CATEGORY", previous_final_cents: 400, previous_margin_bps: 3100, already_under_floor: false }],
    evaluated: 12, not_graded: 1, no_cost: 2, truncated: false,
  };
  const p = readFloorPreview(good);
  assert.equal(p?.items[0]?.sku, "HPV-60");
  assert.equal(p?.items[0]?.alreadyUnderFloor, false);
  assert.equal(p?.evaluated, 12);
  assert.equal(p?.notGraded, 1);
  assert.equal(p?.noCost, 2);
  assert.equal(readFloorPreview({ ...good, no_cost: undefined }), null, "the count of products with no cost is part of the contract");
  assert.equal(readFloorPreview({ ...good, truncated: undefined }), null);
  assert.equal(readFloorPreview({ ...good, items: [{ ...good.items[0], margin_bps: "8" }] }), null);
  assert.equal(readFloorPreview({ ...good, evaluated: 1.5 }), null);
});

test("a list answered as a bare array (what the core writes today) reads the same as the contract's page", () => {
  const bare = readRulePage([rule]);
  assert.equal(bare?.items[0]?.id, "r1");
  assert.equal(bare?.nextCursor, null);
  assert.equal(readPolicyPage([{ category_id: CAT, margin_target_bps: 3400, margin_floor_bps: 2000, margin_enforcement_disabled: false }])?.items.length, 1);
  assert.equal(readOverridePage([override])?.items.length, 1);
  assert.equal(readBreakPage([brk])?.items.length, 1);
  assert.equal(readAuditPage([audit])?.items.length, 1);
  assert.equal(readRulePage([])?.items.length, 0, "an empty list is an empty list, not a refusal");
  assert.equal(readRulePage([rule, { nope: true }]), null, "one unreadable row still refuses the whole list");
  assert.equal(readRulePage("not a list"), null);
});
