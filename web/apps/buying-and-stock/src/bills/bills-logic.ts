// The bills area's plain logic: the words for the match states and what each line's variance means
// (the server's figures, said; nothing here recomputes a variance, a total or a tolerance), the
// check made before a bill is entered, the confirm steps for posting a bill and for accepting a
// variance, and the match rules a super admin sets. Amounts a person types become cents through
// the decimal string (purch-money.ts); quantities stay decimals.
//
// Port provenance: the bill list, the entry form and the three way match view follow the original
// app's purchasing console (repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab: the bills and match panels); the words and the
// confirm steps are HH's.
import type {
  PurchasingBill,
  PurchasingBillPage,
  PurchasingCreateBillRequest,
  PurchasingSetPolicyRequest,
  PurchasingSetToleranceRequest,
  PurchasingTolerance,
} from "@hardscapehouse/api-client";
import { amountCents } from "../landed/landed-logic.js";
import { centsToField, formatMoney, formatQty, parseQty, parseRate, QTY_PLACEHOLDER, MONEY_PLACEHOLDER } from "../purch/purch-money.js";
import { validateExpectedDate, type Tone } from "../po/po-logic.js";

/** The bill list, which the core answers as {"items":null} when there is none. */
export function billRows(page: Partial<Omit<PurchasingBillPage, "items">> & { items?: PurchasingBill[] | null }): PurchasingBill[] {
  return page.items ?? [];
}

// ---- states -----------------------------------------------------------------------------------------------

const MATCH_WORD: Record<string, string> = { MATCHED: "Matched", VARIANCE: "Variance", SHORT_BILLED: "Short billed", UNMATCHED: "Unmatched" };

export function matchWord(state: string): string {
  return MATCH_WORD[state] ?? state;
}

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function matchTone(state: string): Tone {
  if (state === "MATCHED") return "good";
  if (state === "VARIANCE") return "stop";
  if (state === "SHORT_BILLED") return "watch";
  return "plain";
}

const BILL_WORD: Record<string, string> = { DRAFT: "Draft", POSTED: "Posted", VOID: "Void" };

export function billStatusWord(status: string): string {
  return BILL_WORD[status] ?? status;
}

export function billStatusTone(status: string): Tone {
  if (status === "POSTED") return "good";
  if (status === "VOID") return "stop";
  return "plain";
}

/** What a line's quantity variance means. The figure is the server's (billed so far minus received). */
export function qtyVarianceWords(variance: number | undefined, uom: string): string {
  if (variance === undefined) return QTY_PLACEHOLDER;
  if (variance > 0) return `${formatQty(variance, uom)} billed that have not arrived`;
  if (variance < 0) return `${formatQty(Math.abs(variance), uom)} received and not yet billed`;
  return "Billed quantity matches what arrived";
}

/** What a line's price variance means. The figure is the server's, in cents. */
export function priceVarianceWords(cents: number | undefined): string {
  if (cents === undefined) return MONEY_PLACEHOLDER;
  if (cents > 0) return `${formatMoney(cents)} over the order's price`;
  if (cents < 0) return `${formatMoney(Math.abs(cents))} under the order's price`;
  return "Price matches the order";
}

// The seats that enter and post bills (the contract puts these routes on the Money Desk's surface,
// level manage). The core's wall stays the authority; this only keeps a read only seat from being
// offered controls it would be refused.
const BILL_SEATS = new Set(["payables_receivables", "financial_controller", "general_manager", "super_admin"]);

export function canWriteBills(roleCode: string): boolean {
  return BILL_SEATS.has(roleCode);
}

export function canSetRules(roleCode: string): boolean {
  return roleCode === "super_admin";
}

// ---- entering a bill ----------------------------------------------------------------------------------------

interface BillableLine {
  readonly qty_received_base: number;
  readonly qty_billed_base: number;
  readonly unit_cost_cents?: number;
}

/** The quantity a bill line starts with: what has arrived and not yet been billed, as a plain decimal. */
export function prefillQty(line: Pick<BillableLine, "qty_received_base" | "qty_billed_base">): string {
  const left = Math.round((line.qty_received_base - line.qty_billed_base) * 10_000) / 10_000;
  return left > 0 ? String(left) : "";
}

/** The unit price a bill line starts with: the order's own, when the caller may see it. */
export function prefillCost(line: Pick<BillableLine, "unit_cost_cents">): string {
  return line.unit_cost_cents === undefined ? "" : centsToField(line.unit_cost_cents);
}

export interface BillText {
  readonly billNo: string;
  readonly billDate: string;
  readonly dueDate: string;
  readonly tax: string;
  readonly currency: string;
  readonly fx: string;
  /** What was written per order line id; a blank quantity leaves the line off the bill. */
  readonly lines: Readonly<Record<string, { qty: string; cost: string }>>;
}

interface OrderForBill {
  readonly id: string;
  readonly lines: readonly { id: string; sku: string; description: string }[];
}

export type BillCheck = { ok: true; body: PurchasingCreateBillRequest } | { ok: false; words: string };

const fail = (words: string): { ok: false; words: string } => ({ ok: false, words });

function day(text: string, what: string): { ok: true; day: string } | { ok: false; words: string } {
  const t = text.trim();
  if (t === "") return { ok: true, day: "" };
  if (!/^\d{4}-\d{2}-\d{2}$/.test(t)) return fail(`Write the ${what} as year, month and day, such as 2031-03-04.`);
  const checked = validateExpectedDate(t);
  return checked.ok ? { ok: true, day: t } : fail(`The ${what} is not a real date.`);
}

/** The bill form's text, checked, as the body the core expects. Nothing is sent when a check fails. */
export function billBody(form: BillText, po: OrderForBill | null): BillCheck {
  if (form.billNo.trim() === "") return fail("Write the vendor's invoice number.");
  if (po === null) return fail("Pick the purchase order the bill is against.");
  const billDate = day(form.billDate, "bill date");
  if (!billDate.ok) return billDate;
  const dueDate = day(form.dueDate, "due date");
  if (!dueDate.ok) return dueDate;
  const tax = amountCents(form.tax, "tax");
  if (!tax.ok) return tax;
  const lines: NonNullable<PurchasingCreateBillRequest["lines"]> = [];
  for (const line of po.lines) {
    const w = form.lines[line.id];
    if (w === undefined || w.qty.trim() === "") continue;
    const qty = parseQty(w.qty);
    if (!qty.ok) return fail(`Write how many ${line.sku} were billed, as a number above zero with four decimals at most.`);
    const cost = w.cost.trim() === "" ? null : amountCents(w.cost, "unit price");
    if (cost === null || !cost.ok) return fail(`Write the unit price of ${line.sku} as dollars and cents, two decimals at most.`);
    lines.push({ po_line_id: line.id, description: line.description, qty_billed_base: qty.value, unit_cost_cents: cost.cents });
  }
  if (lines.length === 0) return fail("A bill needs at least one line: write a quantity on at least one.");
  const usd = form.currency === "USD";
  const body: PurchasingCreateBillRequest = { bill_no: form.billNo.trim(), po_id: po.id, lines, currency: usd ? "USD" : "CAD" };
  if (billDate.day !== "") body.bill_date = billDate.day;
  if (dueDate.day !== "") body.due_date = dueDate.day;
  if (tax.cents > 0) body.tax_cents = tax.cents;
  if (usd) {
    const rate = parseRate(form.fx);
    if (!rate.ok) return fail("Write the vendor's exchange rate as a positive number, six decimals at most, such as 1.352.");
    body.fx_rate = rate.value;
  }
  return { ok: true, body };
}

// ---- posting ----------------------------------------------------------------------------------------------------

/** The step before a bill is posted: the bill, the vendor, the order and the server's total. */
export function postConfirmWords(bill: Pick<PurchasingBill, "bill_no" | "vendor_name" | "po_no" | "total_cents" | "match_state">): string {
  return `Post bill ${bill.bill_no} from ${bill.vendor_name} for ${formatMoney(bill.total_cents)} against ${bill.po_no}. This books the amount owed to the vendor and relieves what was received but not yet billed. It cannot be edited after.`;
}

/** The core refuses a variance bill without an acceptance, naming the difference in cents: read it. */
export function varianceRefusal(refusal: string): number | null {
  const m = refusal.match(/is in VARIANCE \((-?\d+) cents against the purchase order\)/);
  return m === null ? null : Number(m[1]);
}

/** The step before a variance is accepted: who it records, and the difference as the core puts it. */
export function acceptWords(bill: Pick<PurchasingBill, "bill_no">, varianceCents: number): string {
  return `Bill ${bill.bill_no} is outside the match tolerance: the core puts the difference at ${formatMoney(Math.abs(varianceCents))} against the purchase order. Accepting records you as the person who accepted it, with your note, and posts the bill.`;
}

/** After a post, what the core now holds against what was meant: null when the bill is posted by this press. */
export function settleBill(after: string, before: string): string | null {
  if (after === "POSTED") return before === "POSTED" ? "Nothing changed: it was already posted." : null;
  if (after === "VOID") return "It was not posted: the core reads the bill as void.";
  return "It is still a draft: the core did not post it.";
}

// ---- the match rules ---------------------------------------------------------------------------------------------

export interface ToleranceText {
  readonly qty: string;
  readonly price: string;
  readonly abs: string;
}

export type ToleranceCheck = { ok: true; body: PurchasingSetToleranceRequest } | { ok: false; words: string };

/** The tolerance the form holds, as the core checks it: not negative, and no more than 25 percent. */
export function toleranceBody(form: ToleranceText, branchId: string): ToleranceCheck {
  const qty = parseQty(form.qty, { allowZero: true });
  const price = parseQty(form.price, { allowZero: true });
  if (!qty.ok || !price.ok) return fail("Write each percentage as a number from 0, four decimals at most.");
  if (qty.value > 25 || price.value > 25) return fail("A match tolerance above 25% is not a tolerance, it is an off switch.");
  const abs = amountCents(form.abs, "price allowance");
  if (!abs.ok) return abs;
  const body: PurchasingSetToleranceRequest = {
    qty_tolerance_pct: qty.value,
    price_tolerance_pct: price.value,
    price_tolerance_abs_cents: abs.cents,
  };
  if (branchId !== "") body.branch_id = branchId;
  return { ok: true, body };
}

/** The tolerance in a sentence, from the server's figures. */
export function toleranceWords(t: PurchasingTolerance): string {
  const who = t.is_branch_override ? "This branch has its own rule." : "This is the dealer default.";
  return `A line matches when its quantity is within ${t.qty_tolerance_pct}% of what was received and its price within ${t.price_tolerance_pct}% of the order's price or ${formatMoney(t.price_tolerance_abs_cents)}, whichever allows more. ${who}`;
}

export interface PolicyText {
  readonly visibility: string;
  readonly fallback: string;
}

export type PolicyCheck = { ok: true; body: PurchasingSetPolicyRequest } | { ok: false; words: string };

export const VISIBILITY = [
  { id: "MANAGER_UP", word: "Managers and up" },
  { id: "ALL_STAFF", word: "All staff" },
  { id: "HIDDEN", word: "Hidden from everyone" },
] as const;

export const FALLBACK = [
  { id: "CONFIRM", word: "Ask first" },
  { id: "REFUSE", word: "Refuse" },
] as const;

export function policyBody(form: PolicyText, branchId: string): PolicyCheck {
  const vis = VISIBILITY.find((v) => v.id === form.visibility);
  if (vis === undefined) return fail("Pick who sees cost: managers and up, all staff, or hidden from everyone.");
  const fb = FALLBACK.find((f) => f.id === form.fallback);
  if (fb === undefined) return fail("Pick what an order does when nobody is tagged for ordering: ask first, or refuse.");
  const body: PurchasingSetPolicyRequest = { cost_visibility: vis.id, untagged_fallback: fb.id };
  if (branchId !== "") body.branch_id = branchId;
  return { ok: true, body };
}
