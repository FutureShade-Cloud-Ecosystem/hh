// The pricing panel's form logic: what each editor sends, what it refuses before
// sending, and the words a row or a tier is read in. The server stays the
// authority (it re-validates every write); these tables only keep a typo from
// costing a round trip, and they never decide who may edit.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  BREAK_SURCHARGE_NOTE,
  breakFormFromRow,
  breakSave,
  emptyBreakForm,
  emptyOverrideForm,
  emptyRuleForm,
  isSurchargeTier,
  overrideFormFromRow,
  overrideSave,
  overrideValueText,
  policyDirty,
  policyDraftFromRow,
  policySave,
  ruleCalcOptions,
  ruleFormFromRow,
  ruleSave,
  ruleValueText,
  setRuleScope,
  tierMark,
  tierValueDisplay,
  schedulesWithUnappliedTiers,
} from "../src/_pricing-forms.js";

const CAT = "ca700000-0000-4000-9000-000000000001";
const CUST = "c0000000-0000-4000-9000-000000000001";
const PROD = "a7000000-0000-4000-9000-000000000001";

const ruleRow = {
  id: "r1", scope: "TIER_CATEGORY", customerId: undefined, customerType: "CONTRACTOR", categoryId: CAT, productId: undefined,
  branchId: undefined, calcType: "PCT_OFF_LIST", priceCents: null, discountBps: 1800, markupBps: null, priceUom: "SF", priority: 40,
  validFrom: "2026-01-02T00:00:00Z", validTo: null, detailText: "Contractor, Pavers", isActive: true, version: 3,
};

// ── rules ──────────────────────────────────────────────────────────────────────

test("a new rule form starts as the tier default, a percent off list", () => {
  const f = emptyRuleForm();
  assert.equal(f.scope, "TIER_DEFAULT");
  assert.equal(f.calcType, "PCT_OFF_LIST");
  assert.equal(f.id, "");
  assert.equal(f.version, 0);
});

test("a fixed price is offered on a customer and product rule alone, and leaving that scope drops it", () => {
  assert.deepEqual([...ruleCalcOptions("CUSTOMER_PRODUCT")], ["PCT_OFF_LIST", "MARKUP_ON_COST", "ABSOLUTE"]);
  assert.deepEqual([...ruleCalcOptions("TIER_CATEGORY")], ["PCT_OFF_LIST", "MARKUP_ON_COST"]);
  const f = { ...emptyRuleForm(), scope: "CUSTOMER_PRODUCT", calcType: "ABSOLUTE" };
  assert.equal(setRuleScope(f, "TIER_DEFAULT").calcType, "PCT_OFF_LIST");
  assert.equal(setRuleScope(f, "CUSTOMER_PRODUCT").calcType, "ABSOLUTE");
});

test("a tier rule sends every scope dimension, the unused ones cleared", () => {
  const f = { ...ruleFormFromRow(ruleRow), valuePct: "18.5" };
  const out = ruleSave(f);
  assert.equal(out.ok, true);
  assert.deepEqual(out.value.write, {
    scope: "TIER_CATEGORY", customer_id: "", customer_type: "CONTRACTOR", category_id: CAT, product_id: "", branch_id: "",
    calc_type: "PCT_OFF_LIST", discount_bps: 1850, price_uom: "SF", priority: 40, detail_text: "Contractor, Pavers", version: 3,
  });
  assert.equal(out.value.id, "r1");
});

test("an edit sends the dates, a blank date as the clear signal", () => {
  const f = { ...ruleFormFromRow(ruleRow), validFrom: "", validTo: "2026-12-31" };
  const out = ruleSave(f);
  assert.equal(out.value.patch.valid_from, "");
  assert.equal(out.value.patch.valid_to, "2026-12-31");
  assert.equal(out.value.patch.version, 3);
  assert.equal(out.value.patch.discount_bps, 1800);
  assert.equal(ruleFormFromRow(ruleRow).validFrom, "2026-01-02");
});

test("a customer and product rule needs both ids as uuids, and a fixed price is cents by digits", () => {
  const base = { ...emptyRuleForm(), scope: "CUSTOMER_PRODUCT", calcType: "ABSOLUTE", priceDollars: "12.34" };
  assert.equal(ruleSave({ ...base, customerId: "x", productId: PROD }).ok, false);
  assert.equal(ruleSave({ ...base, customerId: CUST, productId: "" }).ok, false);
  const out = ruleSave({ ...base, customerId: CUST, productId: PROD });
  assert.equal(out.ok, true);
  assert.equal(out.value.write.price_cents, 1234);
  assert.equal(out.value.write.customer_id, CUST);
  assert.equal(out.value.write.category_id, "");
  assert.equal("discount_bps" in out.value.write, false);
});

test("a rule form refuses what it can see is wrong, in words", () => {
  const f = emptyRuleForm();
  assert.match(ruleSave({ ...f, valuePct: "" }).error, /percentage/i);
  assert.match(ruleSave({ ...f, valuePct: "1.005" }).error, /percentage/i);
  assert.match(ruleSave({ ...f, valuePct: "10", priority: "x" }).error, /priority/i);
  assert.match(ruleSave({ ...f, valuePct: "10", priceUom: "" }).error, /unit/i);
  assert.match(ruleSave({ ...f, scope: "TIER_DEFAULT", calcType: "ABSOLUTE", priceDollars: "5" }).error, /fixed price/i);
  assert.match(ruleSave({ ...ruleFormFromRow(ruleRow), validFrom: "01/02/2026" }).error, /date/i);
});

test("a rule's value and key read as the list shows them", () => {
  assert.equal(ruleValueText(ruleRow), "18.00% off");
  assert.equal(ruleValueText({ ...ruleRow, calcType: "MARKUP_ON_COST", discountBps: null, markupBps: 2500 }), "+25.00% on cost");
  assert.equal(ruleValueText({ ...ruleRow, calcType: "ABSOLUTE", discountBps: null, priceCents: 1999 }), "CA$19.99 / SF");
});

// ── customer prices ────────────────────────────────────────────────────────────

test("an override is a percent off list or a markup on cost, never a fixed price", () => {
  const f = { ...emptyOverrideForm(), categoryId: CAT, valuePct: "22" };
  const out = overrideSave(f);
  assert.deepEqual(out.value, { category_id: CAT, calc_type: "PCT_OFF_LIST", discount_bps: 2200, price_uom: "SF", notes: "", version: 0 });
  const m = overrideSave({ ...f, calcType: "MARKUP_ON_COST", valuePct: "30", notes: "agreed" });
  assert.equal(m.value.markup_bps, 3000);
  assert.equal("discount_bps" in m.value, false);
  assert.equal(m.value.notes, "agreed");
  assert.match(overrideSave({ ...f, valuePct: "" }).error, /percentage/i);
});

test("an override edit carries the version it read and shows its percent", () => {
  const row = { id: "o1", customerId: CUST, categoryId: CAT, calcType: "PCT_OFF_LIST", discountBps: 2200, markupBps: null, priceUom: "SF", notes: "n", version: 4 };
  const f = overrideFormFromRow(row);
  assert.equal(f.valuePct, "22.00");
  assert.equal(overrideSave(f).value.version, 4);
  assert.equal(overrideValueText(row), "22.00% off");
  assert.equal(overrideValueText({ ...row, calcType: "MARKUP_ON_COST", discountBps: null, markupBps: 1000 }), "+10.00% on cost");
});

// ── quantity breaks ────────────────────────────────────────────────────────────

const breakRow = {
  id: "b1", code: "PAVER-FULL-SKID", name: "Full skid", productId: undefined, categoryId: CAT, thresholdUom: "PLT", isActive: true, version: 2,
  items: [
    { fromQty: 1, toQty: null, valueType: "PCT_OFF", discountBps: 500, discountCents: null, surchargeBps: null, surchargeCents: null, priceUom: "SF" },
    { fromQty: 0, toQty: 2000, valueType: "SURCHARGE_PCT", discountBps: null, discountCents: null, surchargeBps: 1000, surchargeCents: null, priceUom: "KG" },
  ],
};

test("a surcharge tier is marked not applied, a discount tier is not", () => {
  assert.equal(isSurchargeTier("SURCHARGE_PCT"), true);
  assert.equal(isSurchargeTier("SURCHARGE_ABS"), true);
  assert.equal(isSurchargeTier("PCT_OFF"), false);
  assert.equal(isSurchargeTier("ABS_OFF"), false);
  assert.equal(tierMark("SURCHARGE_PCT"), "not applied");
  assert.equal(tierMark("SURCHARGE_ABS"), "not applied");
  assert.equal(tierMark("PCT_OFF"), null);
  assert.equal(tierMark("ABS_OFF"), null);
});

test("the note says the whole of it: never changes a price, why, and that nothing is deleted", () => {
  assert.match(BREAK_SURCHARGE_NOTE, /never change a price/i);
  assert.match(BREAK_SURCHARGE_NOTE, /cheaper/i);
  assert.match(BREAK_SURCHARGE_NOTE, /nothing is deleted/i);
  assert.equal(/[–—]/.test(BREAK_SURCHARGE_NOTE), false);
});

test("schedules with an unapplied tier are counted so the note can name how many", () => {
  assert.equal(schedulesWithUnappliedTiers([breakRow]), 1);
  assert.equal(schedulesWithUnappliedTiers([{ ...breakRow, items: [breakRow.items[0]] }]), 0);
  assert.equal(schedulesWithUnappliedTiers([]), 0);
});

test("a tier's adjustment reads signed, discounts down and surcharges up", () => {
  assert.equal(tierValueDisplay(breakRow.items[0]), "-5.00%");
  assert.equal(tierValueDisplay(breakRow.items[1]), "+10.00%");
  assert.equal(tierValueDisplay({ ...breakRow.items[0], valueType: "ABS_OFF", discountCents: 250 }), "-CA$2.50");
  assert.equal(tierValueDisplay({ ...breakRow.items[1], valueType: "SURCHARGE_ABS", surchargeCents: 99 }), "+CA$0.99");
});

test("a break form round trips a schedule and sends each tier's own value field", () => {
  const f = breakFormFromRow(breakRow);
  assert.equal(f.scopeKind, "category");
  assert.equal(f.tiers[0].valueInput, "5.00");
  assert.equal(f.tiers[1].toQty, "2000");
  const out = breakSave(f);
  assert.equal(out.ok, true);
  assert.deepEqual(out.value.items[0], { from_qty: 1, to_qty: null, value_type: "PCT_OFF", price_uom: "SF", discount_bps: 500 });
  assert.deepEqual(out.value.items[1], { from_qty: 0, to_qty: 2000, value_type: "SURCHARGE_PCT", price_uom: "KG", surcharge_bps: 1000 });
  assert.equal(out.value.category_id, CAT);
  assert.equal(out.value.product_id, "");
  assert.equal(out.value.version, 2);
});

test("a break form refuses what it can see is wrong", () => {
  const ok = { ...emptyBreakForm(), code: "C", name: "N", categoryId: CAT, tiers: [{ fromQty: "1", toQty: "", valueType: "PCT_OFF", valueInput: "5", priceUom: "SF" }] };
  assert.equal(breakSave(ok).ok, true);
  assert.match(breakSave({ ...ok, code: " " }).error, /code and name/i);
  assert.match(breakSave({ ...ok, scopeKind: "product", productId: "x" }).error, /product/i);
  assert.match(breakSave({ ...ok, tiers: [] }).error, /tier/i);
  assert.match(breakSave({ ...ok, tiers: [{ ...ok.tiers[0], fromQty: "a" }] }).error, /tier 1.*from/i);
  assert.match(breakSave({ ...ok, tiers: [{ ...ok.tiers[0], valueInput: "" }] }).error, /tier 1.*percentage/i);
  assert.match(breakSave({ ...ok, tiers: [{ ...ok.tiers[0], valueType: "ABS_OFF", valueInput: "x" }] }).error, /tier 1.*dollar/i);
});

// ── category margin policy ─────────────────────────────────────────────────────

test("a policy row's draft arms its Save only when it differs", () => {
  const row = { categoryId: CAT, targetBps: 3400, floorBps: 2000, enforcementOff: false };
  const d = policyDraftFromRow(row);
  assert.deepEqual(d, { target: "34.00", floor: "20.00", enforce: true });
  assert.equal(policyDirty(row, d), false);
  assert.equal(policyDirty(row, { ...d, target: "35.00" }), true);
  assert.equal(policyDirty(row, { ...d, enforce: false }), true);
});

test("a policy save needs two percentages and a floor not above the target", () => {
  const out = policySave({ target: "34", floor: "20.5", enforce: false });
  assert.deepEqual(out.value, { margin_target_bps: 3400, margin_floor_bps: 2050, margin_enforcement_disabled: true });
  assert.match(policySave({ target: "", floor: "20", enforce: true }).error, /target and floor/i);
  assert.match(policySave({ target: "10", floor: "20", enforce: true }).error, /floor must be at or below the target/i);
});
