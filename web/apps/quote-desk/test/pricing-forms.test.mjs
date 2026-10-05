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
  ruleAppliesTo,
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

// The core stores a typed end date as midnight of the NEXT day (handler.go parseValidityBound: the
// upper bound is exclusive), so a form that reads the stored day back must give the day back.
const coreStoresValidTo = (day) => new Date(Date.parse(`${day}T00:00:00Z`) + 86400000).toISOString().replace(".000Z", "Z");

test("an end date survives an edit: what the core stored reads back as the day the editor typed", () => {
  for (const typed of ["2026-11-15", "2026-12-31", "2027-03-01", "2028-02-28"]) {
    const stored = coreStoresValidTo(typed);
    const form = ruleFormFromRow({ ...ruleRow, validTo: stored });
    assert.equal(form.validTo, typed, `${stored} is the ${typed} typed`);
    const out = ruleSave({ ...form, detailText: "only the text changed" });
    assert.equal(out.value.patch.valid_to, typed, "the unchanged date goes back as the same day");
    assert.equal(coreStoresValidTo(out.value.patch.valid_to), stored, "and the core stores the same instant again");
  }
  assert.equal(ruleFormFromRow({ ...ruleRow, validTo: null }).validTo, "");
  assert.equal(ruleFormFromRow({ ...ruleRow, validTo: "2026-11-16T09:30:00Z" }).validTo, "2026-11-16", "an instant inside a day shows that day");
});

test("a customer and category rule keeps its customer and category through an edit", () => {
  const row = { ...ruleRow, scope: "CUSTOMER_CATEGORY", customerId: CUST, customerType: undefined, categoryId: CAT };
  const form = ruleFormFromRow(row);
  assert.equal(form.scope, "CUSTOMER_CATEGORY");
  const out = ruleSave({ ...form, valuePct: "21" });
  assert.equal(out.ok, true);
  assert.equal(out.value.patch.scope, "CUSTOMER_CATEGORY");
  assert.equal(out.value.patch.customer_id, CUST);
  assert.equal(out.value.patch.category_id, CAT);
  assert.equal(out.value.patch.customer_type, "");
  assert.equal(out.value.patch.discount_bps, 2100);
  assert.match(ruleSave({ ...form, valuePct: "21", customerId: "x" }).error, /customer/i);
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
  for (const odd of ["1e3", "0x10", "1.5", "-2", "+3", "١٢"]) assert.match(ruleSave({ ...f, valuePct: "10", priority: odd }).error, /priority/i, `priority ${odd}`);
  assert.equal(ruleSave({ ...f, valuePct: "10", priority: "40" }).value.write.priority, 40);
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
  assert.equal(/[\u2013\u2014]/.test(BREAK_SURCHARGE_NOTE), false);
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

test("a rule's applies to reads as the list shows it", () => {
  const branch = (id) => (id === "b1" ? "Trenton" : id.slice(0, 8));
  const cat = (id) => (id === CAT ? "Pavers" : id.slice(0, 8));
  assert.equal(ruleAppliesTo(ruleRow, branch, cat), "CONTRACTOR · Pavers");
  assert.equal(ruleAppliesTo({ ...ruleRow, branchId: "b1" }, branch, cat), "CONTRACTOR · Pavers · Trenton");
  assert.equal(ruleAppliesTo({ ...ruleRow, scope: "TIER_DEFAULT", categoryId: undefined }, branch, cat), "CONTRACTOR");
  assert.equal(ruleAppliesTo({ ...ruleRow, scope: "CUSTOMER_PRODUCT", customerId: CUST, productId: PROD }, branch, cat), "c0000000 · a7000000");
});

// The reviewer's P2-C case: a rule of any scope can be limited to one branch (the server's
// patch clears it only on an empty string), so the save must carry the branch through for
// every scope, not just the tier and category one whose editor can change it.
test("a rule's branch is carried through for every scope", () => {
  const BRANCH = "6f1c2b3a-0000-4000-8000-000000000002";
  for (const scope of ["TIER_DEFAULT", "TIER_CATEGORY", "CUSTOMER_CATEGORY", "CUSTOMER_PRODUCT"]) {
    const row = { ...ruleRow, scope, branchId: BRANCH, ...(scope === "CUSTOMER_CATEGORY" || scope === "CUSTOMER_PRODUCT" ? { customerId: CUST } : {}), ...(scope === "CUSTOMER_PRODUCT" ? { productId: PROD } : {}) };
    const out = ruleSave(ruleFormFromRow(row));
    assert.equal(out.ok, true, scope);
    assert.equal(out.value.write.branch_id, BRANCH, `${scope} write carries the branch`);
    assert.equal(out.value.patch.branch_id, BRANCH, `${scope} patch carries the branch`);
  }
  // A rule with no branch still sends the empty string, the clear signal.
  const none = ruleSave(ruleFormFromRow({ ...ruleRow, branchId: undefined }));
  assert.equal(none.value.write.branch_id, "");
});

// The reviewer's round 2 note: a core not hosted in UTC serves the stored instant with its own
// offset (a typed 15th shows as 2026-11-14T16:00:00-08:00), and slicing that string moved the
// shown day one day earlier on every edit. The day is the UTC date part of the instant, the
// way the core wrote it; the end date already reads it that way.
test("a rule's start date is read as the day the core wrote, whatever offset it is served in", () => {
  assert.equal(ruleFormFromRow({ ...ruleRow, validFrom: "2026-11-14T16:00:00-08:00" }).validFrom, "2026-11-15");
  assert.equal(ruleFormFromRow({ ...ruleRow, validFrom: "2026-11-15T00:00:00Z" }).validFrom, "2026-11-15");
  assert.equal(ruleFormFromRow({ ...ruleRow, validFrom: "2026-11-15T05:30:00+05:30" }).validFrom, "2026-11-15");
  assert.equal(ruleFormFromRow({ ...ruleRow, validFrom: null }).validFrom, "");
  assert.equal(ruleFormFromRow({ ...ruleRow, validFrom: "not a date" }).validFrom, "not a date");
});
