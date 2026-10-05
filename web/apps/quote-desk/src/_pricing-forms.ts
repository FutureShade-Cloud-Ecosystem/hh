// Ported from hardscapeos_dibbits master 87d7941b app/src/rule-editor.ts, customer-pricing.ts,
// quantity-breaks.ts and margin-grid.ts (their form logic); adapted for HH.
//
// _pricing-forms.ts: what each editor of the pricing panel sends, what it refuses before
// sending, and the words a row or a tier is read in (DOM-free, unit tested on the Node
// runner). The server stays the authority: it re-validates every write and decides who
// may make it. These tables only keep a typo from costing a round trip, and they never
// decide a role rule.
//
// CP0-8 (answer A) lives here too: a surcharge tier never changes a price under the
// replace rule, so the break editor says so and marks each such tier "not applied".
// Nothing is deleted or hidden.
import type {
  PricingCategoryPolicyWrite,
  PricingOverrideWrite,
  PricingQuantityBreakItem,
  PricingQuantityBreakWrite,
  PricingRulePatch,
  PricingRuleWrite,
} from "@hardscapehouse/api-client";
import { bpsText, centsText, dollarsToCents, formatMoney, formatPct, pctToBps } from "./_pricing-shared.js";
import type { BreakItemRow, BreakRow, OverrideRow, PolicyRow, RuleRow } from "./_pricing-wire.js";

export type FormOutcome<T> = { ok: true; value: T } | { ok: false; error: string };

const fail = <T>(error: string): FormOutcome<T> => ({ ok: false, error });
const good = <T>(value: T): FormOutcome<T> => ({ ok: true, value });

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/;

function isRealDay(text: string): boolean {
  if (!ISO_DAY.test(text)) return false;
  const d = new Date(`${text}T00:00:00Z`);
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === text;
}

// ── rules ────────────────────────────────────────────────────────────────────

export type RuleScope = "CUSTOMER_PRODUCT" | "CUSTOMER_CATEGORY" | "TIER_CATEGORY" | "TIER_DEFAULT";
export type RuleCalc = "ABSOLUTE" | "PCT_OFF_LIST" | "MARKUP_ON_COST";

/** The three scopes the editor can create; a customer and category price belongs to the Customer prices tab. */
export const EDITABLE_SCOPES = ["TIER_DEFAULT", "TIER_CATEGORY", "CUSTOMER_PRODUCT"] as const;

export interface RuleForm {
  id: string;
  scope: RuleScope;
  customerType: string;
  customerId: string;
  categoryId: string;
  productId: string;
  branchId: string;
  calcType: RuleCalc;
  valuePct: string;
  priceDollars: string;
  priceUom: string;
  priority: string;
  validFrom: string;
  validTo: string;
  detailText: string;
  version: number;
}

export function emptyRuleForm(): RuleForm {
  return {
    id: "",
    scope: "TIER_DEFAULT",
    customerType: "CONTRACTOR",
    customerId: "",
    categoryId: "ca700000-0000-4000-9000-000000000001",
    productId: "",
    branchId: "",
    calcType: "PCT_OFF_LIST",
    valuePct: "",
    priceDollars: "",
    priceUom: "EA",
    priority: "0",
    validFrom: "",
    validTo: "",
    detailText: "",
    version: 0,
  };
}

/**
 * The day an editor typed as a rule's end date, read back from what the core stored. The core
 * keeps a typed end date as midnight of the NEXT day (the upper bound is exclusive, so the
 * typed day is inclusive; handler.go parseValidityBound), so a stored midnight shows as the
 * day before. An instant inside a day shows that day. Reading the stored day as it stands
 * would move the end date one day later on every edit.
 */
export function validToDay(stored: string | null | undefined): string {
  if (stored === null || stored === undefined || stored === "") return "";
  const at = new Date(stored);
  if (Number.isNaN(at.getTime())) return stored.slice(0, 10);
  const day = at.toISOString().slice(0, 10);
  const atMidnight = at.getUTCHours() === 0 && at.getUTCMinutes() === 0 && at.getUTCSeconds() === 0 && at.getUTCMilliseconds() === 0;
  return atMidnight ? new Date(at.getTime() - 86400000).toISOString().slice(0, 10) : day;
}

/**
 * A rule's start date, read back from what the core stored. The core keeps a typed start day
 * as that day's midnight UTC, so the day is the UTC date part of the served instant; a core
 * not hosted in UTC serves the same instant with its own offset (a typed 15th as
 * 2026-11-14T16:00:00-08:00) and slicing that string would move the shown day one day
 * earlier on every edit.
 */
export function validFromDay(stored: string | null | undefined): string {
  if (stored === null || stored === undefined || stored === "") return "";
  const at = new Date(stored);
  if (Number.isNaN(at.getTime())) return stored.slice(0, 10);
  return at.toISOString().slice(0, 10);
}

export function ruleFormFromRow(r: RuleRow): RuleForm {
  const base = emptyRuleForm();
  return {
    id: r.id,
    scope: r.scope,
    customerType: r.customerType ?? base.customerType,
    customerId: r.customerId ?? "",
    categoryId: r.categoryId ?? base.categoryId,
    productId: r.productId ?? "",
    branchId: r.branchId ?? "",
    calcType: r.calcType,
    valuePct: r.calcType === "PCT_OFF_LIST" ? bpsText(r.discountBps ?? 0) : r.calcType === "MARKUP_ON_COST" ? bpsText(r.markupBps ?? 0) : "",
    priceDollars: r.calcType === "ABSOLUTE" ? centsText(r.priceCents ?? 0) : "",
    priceUom: r.priceUom,
    priority: String(r.priority),
    validFrom: validFromDay(r.validFrom),
    validTo: validToDay(r.validTo),
    detailText: r.detailText,
    version: r.version,
  };
}

/** A fixed price is a customer and product negotiation; a tier or category rule is a percentage. */
export function ruleCalcOptions(scope: RuleScope | string): readonly RuleCalc[] {
  return scope === "CUSTOMER_PRODUCT" ? ["PCT_OFF_LIST", "MARKUP_ON_COST", "ABSOLUTE"] : ["PCT_OFF_LIST", "MARKUP_ON_COST"];
}

/** Changing the scope; leaving the customer and product scope drops a fixed price. */
export function setRuleScope(form: RuleForm, scope: RuleScope): RuleForm {
  const calcType = scope !== "CUSTOMER_PRODUCT" && form.calcType === "ABSOLUTE" ? "PCT_OFF_LIST" : form.calcType;
  return { ...form, scope, calcType };
}

export interface RuleSave {
  /** "" on a create. */
  id: string;
  write: PricingRuleWrite;
  patch: PricingRulePatch;
}

function calcFields(form: RuleForm): FormOutcome<{ price_cents?: number; discount_bps?: number; markup_bps?: number }> {
  if (form.calcType === "ABSOLUTE") {
    const cents = dollarsToCents(form.priceDollars);
    return cents === null ? fail("Enter a fixed price in dollars and cents.") : good({ price_cents: cents });
  }
  const bps = pctToBps(form.valuePct);
  if (bps === null) return fail("Enter a percentage with at most two decimals.");
  return good(form.calcType === "PCT_OFF_LIST" ? { discount_bps: bps } : { markup_bps: bps });
}

export function ruleSave(form: RuleForm): FormOutcome<RuleSave> {
  if (form.calcType === "ABSOLUTE" && form.scope !== "CUSTOMER_PRODUCT") {
    return fail("A fixed price is only allowed on a customer and product rule.");
  }
  const calc = calcFields(form);
  if (!calc.ok) return calc;
  const priorityText = form.priority.trim();
  if (priorityText !== "" && !/^[0-9]+$/.test(priorityText)) return fail("Priority must be a whole number, digits only.");
  const priority = priorityText === "" ? 0 : Number(priorityText);
  if (!Number.isSafeInteger(priority)) return fail("Priority must be a whole number, digits only.");
  if (form.priceUom.trim() === "") return fail("Enter the price unit.");
  if (form.scope === "CUSTOMER_PRODUCT") {
    if (!UUID.test(form.customerId.trim())) return fail("A customer and product rule needs the customer id as a uuid.");
    if (!UUID.test(form.productId.trim())) return fail("A customer and product rule needs the product id as a uuid.");
  }
  if (form.scope === "CUSTOMER_CATEGORY") {
    if (!UUID.test(form.customerId.trim())) return fail("A customer and category rule needs the customer id as a uuid.");
    if (form.categoryId.trim() === "") return fail("A customer and category rule needs a category.");
  }
  if (form.id !== "") {
    for (const day of [form.validFrom, form.validTo]) {
      if (day !== "" && !isRealDay(day)) return fail("Enter the valid dates as a real calendar date.");
    }
  }
  // Every scope dimension is sent, the ones that do not belong to the scope as "" so the server clears a stale value.
  // The branch is the one exception on every scope: a rule of any scope can be limited to one
  // branch (the resolver honours it), and the field is editable on the tier and category scope
  // alone, so the other scopes carry what the rule already has or the edit would silently widen
  // it to every branch.
  const isTier = form.scope === "TIER_DEFAULT" || form.scope === "TIER_CATEGORY";
  const dims = {
    customer_type: isTier ? form.customerType : "",
    category_id: form.scope === "TIER_CATEGORY" || form.scope === "CUSTOMER_CATEGORY" ? form.categoryId : "",
    branch_id: form.branchId,
    customer_id: form.scope === "CUSTOMER_PRODUCT" || form.scope === "CUSTOMER_CATEGORY" ? form.customerId.trim() : "",
    product_id: form.scope === "CUSTOMER_PRODUCT" ? form.productId.trim() : "",
  };
  const write: PricingRuleWrite = {
    scope: form.scope,
    ...dims,
    calc_type: form.calcType,
    ...calc.value,
    price_uom: form.priceUom.trim(),
    priority,
    detail_text: form.detailText,
    version: form.version,
  };
  const patch: PricingRulePatch = {
    scope: form.scope,
    ...dims,
    calc_type: form.calcType,
    ...calc.value,
    price_uom: form.priceUom.trim(),
    priority,
    detail_text: form.detailText,
    valid_from: form.validFrom,
    valid_to: form.validTo,
    version: form.version,
  };
  return good({ id: form.id, write, patch });
}

/** A rule's value as the list reads it. */
export function ruleValueText(r: Pick<RuleRow, "calcType" | "discountBps" | "markupBps" | "priceCents" | "priceUom">): string {
  if (r.calcType === "PCT_OFF_LIST") return `${formatPct(r.discountBps ?? 0)} off`;
  if (r.calcType === "MARKUP_ON_COST") return `+${formatPct(r.markupBps ?? 0)} on cost`;
  return `${formatMoney(r.priceCents ?? 0)} / ${r.priceUom}`;
}

/** What a rule applies to, as the list says it; ids shorten to eight characters, and a named branch is named. */
export function ruleAppliesTo(r: Pick<RuleRow, "scope" | "customerId" | "productId" | "customerType" | "categoryId" | "branchId">, branchName: (id: string) => string, categoryName: (id: string) => string): string {
  // A rule of any scope can be limited to one branch, so the branch is named wherever it is set.
  const at = r.branchId === undefined ? "" : ` \u00b7 ${branchName(r.branchId)}`;
  if (r.scope === "CUSTOMER_PRODUCT") return `${(r.customerId ?? "").slice(0, 8)} \u00b7 ${(r.productId ?? "").slice(0, 8)}${at}`;
  if (r.scope === "CUSTOMER_CATEGORY") return `${(r.customerId ?? "").slice(0, 8)} \u00b7 ${categoryName(r.categoryId ?? "")}${at}`;
  if (r.scope === "TIER_CATEGORY") {
    return `${r.customerType ?? ""} \u00b7 ${categoryName(r.categoryId ?? "")}${at}`;
  }
  return `${r.customerType ?? ""}${at}`;
}

// ── customer prices ──────────────────────────────────────────────────────────

export interface OverrideForm {
  id: string;
  categoryId: string;
  calcType: "PCT_OFF_LIST" | "MARKUP_ON_COST";
  valuePct: string;
  priceUom: string;
  notes: string;
  version: number;
}

export function emptyOverrideForm(): OverrideForm {
  return { id: "", categoryId: "ca700000-0000-4000-9000-000000000001", calcType: "PCT_OFF_LIST", valuePct: "", priceUom: "SF", notes: "", version: 0 };
}

export function overrideFormFromRow(o: OverrideRow): OverrideForm {
  return {
    id: o.id,
    categoryId: o.categoryId,
    calcType: o.calcType,
    valuePct: bpsText(o.calcType === "MARKUP_ON_COST" ? (o.markupBps ?? 0) : (o.discountBps ?? 0)),
    priceUom: o.priceUom,
    notes: o.notes,
    version: o.version,
  };
}

export function overrideSave(form: OverrideForm): FormOutcome<PricingOverrideWrite> {
  const bps = pctToBps(form.valuePct);
  if (bps === null) return fail("Enter a percentage with at most two decimals.");
  if (form.priceUom.trim() === "") return fail("Enter the price unit.");
  return good({
    category_id: form.categoryId,
    calc_type: form.calcType,
    ...(form.calcType === "MARKUP_ON_COST" ? { markup_bps: bps } : { discount_bps: bps }),
    price_uom: form.priceUom.trim(),
    notes: form.notes,
    version: form.version,
  });
}

export function overrideValueText(o: Pick<OverrideRow, "calcType" | "discountBps" | "markupBps">): string {
  return o.calcType === "MARKUP_ON_COST" ? `+${formatPct(o.markupBps ?? 0)} on cost` : `${formatPct(o.discountBps ?? 0)} off`;
}

// ── quantity breaks ──────────────────────────────────────────────────────────

export type BreakValueType = "PCT_OFF" | "ABS_OFF" | "SURCHARGE_PCT" | "SURCHARGE_ABS";

export const BREAK_VALUE_CHOICES: ReadonlyArray<{ value: BreakValueType; label: string; unit: "pct" | "abs" }> = [
  { value: "PCT_OFF", label: "Discount %", unit: "pct" },
  { value: "ABS_OFF", label: "Discount $", unit: "abs" },
  { value: "SURCHARGE_PCT", label: "Surcharge % (not applied)", unit: "pct" },
  { value: "SURCHARGE_ABS", label: "Surcharge $ (not applied)", unit: "abs" },
];

export const BREAK_UOMS = ["EA", "PC", "SF", "PLT", "LF", "KG", "T", "BAG"] as const;

/**
 * The note CP0-8 asks for. A surcharge tier is an uplift, and under the replace rule a
 * break replaces the price only when it is cheaper, so an uplift can never win.
 */
export const BREAK_SURCHARGE_NOTE =
  "Surcharge tiers never change a price. A quantity break replaces the price only when it is cheaper, " +
  "and a surcharge is never cheaper, so quotes and the counter ignore these tiers. Each is marked " +
  "not applied below. They stay as configured: nothing is deleted.";

export function isSurchargeTier(valueType: string): boolean {
  return valueType === "SURCHARGE_PCT" || valueType === "SURCHARGE_ABS";
}

/** The mark a tier wears in the list and the editor: surcharge tiers are "not applied". */
export function tierMark(valueType: string): "not applied" | null {
  return isSurchargeTier(valueType) ? "not applied" : null;
}

/** How many schedules hold at least one tier that is not applied. */
export function schedulesWithUnappliedTiers(rows: ReadonlyArray<Pick<BreakRow, "items">>): number {
  return rows.filter((r) => r.items.some((it) => isSurchargeTier(it.valueType))).length;
}

/** A tier's adjustment, signed: discounts down, surcharges up. */
export function tierValueDisplay(it: Pick<BreakItemRow, "valueType" | "discountBps" | "discountCents" | "surchargeBps" | "surchargeCents">): string {
  switch (it.valueType) {
    case "PCT_OFF":
      return `-${formatPct(it.discountBps ?? 0)}`;
    case "ABS_OFF":
      return `-${formatMoney(it.discountCents ?? 0)}`;
    case "SURCHARGE_PCT":
      return `+${formatPct(it.surchargeBps ?? 0)}`;
    case "SURCHARGE_ABS":
      return `+${formatMoney(it.surchargeCents ?? 0)}`;
  }
}

function tierValueInput(it: BreakItemRow): string {
  switch (it.valueType) {
    case "PCT_OFF":
      return bpsText(it.discountBps ?? 0);
    case "ABS_OFF":
      return centsText(it.discountCents ?? 0);
    case "SURCHARGE_PCT":
      return bpsText(it.surchargeBps ?? 0);
    case "SURCHARGE_ABS":
      return centsText(it.surchargeCents ?? 0);
  }
}

export interface TierForm {
  fromQty: string;
  toQty: string;
  valueType: BreakValueType;
  valueInput: string;
  priceUom: string;
}

export interface BreakForm {
  id: string;
  code: string;
  name: string;
  scopeKind: "category" | "product";
  categoryId: string;
  productId: string;
  thresholdUom: string;
  tiers: TierForm[];
  version: number;
}

export function emptyTierForm(): TierForm {
  return { fromQty: "0", toQty: "", valueType: "PCT_OFF", valueInput: "", priceUom: "EA" };
}

export function emptyBreakForm(): BreakForm {
  return {
    id: "",
    code: "",
    name: "",
    scopeKind: "category",
    categoryId: "ca700000-0000-4000-9000-000000000001",
    productId: "",
    thresholdUom: "PLT",
    tiers: [emptyTierForm()],
    version: 0,
  };
}

export function breakFormFromRow(h: BreakRow): BreakForm {
  return {
    id: h.id,
    code: h.code,
    name: h.name,
    scopeKind: h.productId !== undefined ? "product" : "category",
    categoryId: h.categoryId ?? "ca700000-0000-4000-9000-000000000001",
    productId: h.productId ?? "",
    thresholdUom: h.thresholdUom,
    tiers: h.items.map((it) => ({
      fromQty: String(it.fromQty),
      toQty: it.toQty === null ? "" : String(it.toQty),
      valueType: it.valueType,
      valueInput: tierValueInput(it),
      priceUom: it.priceUom,
    })),
    version: h.version,
  };
}

const QTY = /^\d+(?:\.\d+)?$/;

export function breakSave(form: BreakForm): FormOutcome<PricingQuantityBreakWrite> {
  if (form.code.trim() === "" || form.name.trim() === "") return fail("Code and name are required.");
  if (form.scopeKind === "product" && !UUID.test(form.productId.trim())) return fail("A product break needs the product id as a uuid.");
  if (form.tiers.length === 0) return fail("Add at least one tier.");
  const items: PricingQuantityBreakItem[] = [];
  for (let i = 0; i < form.tiers.length; i += 1) {
    const t = form.tiers[i];
    if (t === undefined) continue;
    const n = i + 1;
    const fromText = t.fromQty.trim() === "" ? "0" : t.fromQty.trim();
    if (!QTY.test(fromText)) return fail(`Tier ${n}: the from quantity must be a number.`);
    const toText = t.toQty.trim();
    if (toText !== "" && !QTY.test(toText)) return fail(`Tier ${n}: the to quantity must be a number or blank.`);
    const item: PricingQuantityBreakItem = {
      from_qty: Number(fromText),
      to_qty: toText === "" ? null : Number(toText),
      value_type: t.valueType,
      price_uom: t.priceUom,
    };
    const choice = BREAK_VALUE_CHOICES.find((c) => c.value === t.valueType);
    if (choice?.unit === "pct") {
      const bps = pctToBps(t.valueInput);
      if (bps === null) return fail(`Tier ${n}: enter a percentage with at most two decimals.`);
      if (isSurchargeTier(t.valueType)) item.surcharge_bps = bps;
      else item.discount_bps = bps;
    } else {
      const cents = dollarsToCents(t.valueInput);
      if (cents === null) return fail(`Tier ${n}: enter a dollar amount with at most two decimals.`);
      if (isSurchargeTier(t.valueType)) item.surcharge_cents = cents;
      else item.discount_cents = cents;
    }
    items.push(item);
  }
  return good({
    code: form.code.trim(),
    name: form.name.trim(),
    category_id: form.scopeKind === "category" ? form.categoryId : "",
    product_id: form.scopeKind === "product" ? form.productId.trim() : "",
    threshold_uom: form.thresholdUom,
    items,
    version: form.version,
  });
}

// ── category margin policy ───────────────────────────────────────────────────

export interface PolicyDraft {
  target: string;
  floor: string;
  enforce: boolean;
}

export function policyDraftFromRow(p: PolicyRow): PolicyDraft {
  return { target: bpsText(p.targetBps), floor: bpsText(p.floorBps), enforce: !p.enforcementOff };
}

/** Whether a row's draft differs from what the server holds: it arms the row's Save. */
export function policyDirty(p: PolicyRow, d: PolicyDraft): boolean {
  const saved = policyDraftFromRow(p);
  return d.target !== saved.target || d.floor !== saved.floor || d.enforce !== saved.enforce;
}

export function policySave(d: PolicyDraft): FormOutcome<PricingCategoryPolicyWrite> {
  const target = pctToBps(d.target);
  const floor = pctToBps(d.floor);
  if (target === null || floor === null) return fail("Enter target and floor as percentages with at most two decimals.");
  if (floor > target) return fail("The floor must be at or below the target.");
  return good({ margin_target_bps: target, margin_floor_bps: floor, margin_enforcement_disabled: !d.enforce });
}
