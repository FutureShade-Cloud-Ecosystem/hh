// The Payables board's own vocabulary: the shape the purchasing route answers, the aging bands
// the board shows in the desk's one vocabulary, and the words a landed cost line wears.
//
// The shape is the contract's own (core/api/fragments/purchasing.yaml, the route
// /api/v1/purchasing/ap-aging and its schemas PurchasingAPAgingPage, PurchasingAPAgingRow and
// PurchasingAPAgingItem, W2-9a). The web client has not been regenerated on this branch yet, so
// the shape is written here by hand, with the schema's names on both the types and the fields.
// When the client is regenerated, this file's types give way to the generated ones, which carry
// the same names: the screens do not change.
//
// Money is the server's: the board reads the figures the route sends and never adds them up, the
// same rule the receivables slice holds (test/no-app-money.test.mjs).
import { formatMoney } from "./ar-money.js";

/** One open AP obligation on a row: a vendor bill, or a landed cost payable to a carrier or broker. */
export type PurchasingAPAgingItem = {
  readonly id: string;
  /** The kind of obligation: a vendor bill, or a landed cost. */
  readonly kind: "VENDOR_BILL" | "LANDED_COST";
  readonly is_landed_cost: boolean;
  /** The document's reference: the bill's number, or the PO reference a landed cost rides on. */
  readonly ref_no: string;
  readonly bill_no?: string;
  readonly po_ref?: string;
  readonly branch_id: string;
  readonly vendor_id?: string;
  readonly vendor_name?: string;
  /** What the landed cost was for: freight, duty or handling. */
  readonly charge_kind?: string;
  readonly bill_date: string;
  readonly due_date: string;
  readonly total_cents: number;
  readonly paid_cents: number;
  readonly open_cents: number;
  /** The document currency, and the total and open amount in it: for a CAD bill they equal the CAD figures. */
  readonly currency?: string;
  readonly doc_total_cents?: number;
  readonly doc_open_cents?: number;
  /** The band the obligation sits in, in the core's key for the purchasing route. */
  readonly bucket: string;
};

/** One row of the AP aging: a vendor with its open bills, or a landed cost payable. */
export type PurchasingAPAgingRow = {
  readonly row_kind: "VENDOR" | "LANDED_COST";
  readonly is_landed_cost: boolean;
  readonly vendor_id?: string;
  readonly vendor_no?: string;
  readonly vendor_name: string;
  readonly landed_cost_id?: string;
  readonly charge_kind?: string;
  readonly due_date?: string;
  readonly payment_terms_days: number;
  readonly current_cents: number;
  readonly days_30_cents: number;
  readonly days_60_cents: number;
  readonly days_60_to_90_cents: number;
  readonly days_90_plus_cents: number;
  /** The row's open total: its bands add up to it. */
  readonly total_cents: number;
  readonly open_items: readonly PurchasingAPAgingItem[];
};

/** The board's read: the rows, the bands of the whole book, and the day it is read as of. */
export type PurchasingAPAgingPage = {
  readonly as_of: string;
  readonly current_cents: number;
  readonly days_30_cents: number;
  readonly days_60_cents: number;
  readonly days_60_to_90_cents: number;
  readonly days_90_plus_cents: number;
  readonly total_cents: number;
  readonly items: readonly PurchasingAPAgingRow[];
  readonly next_cursor: string | null;
};

/** The board's own state: the read, the day it is read as of, and the row open. */
export interface PayablesBoardModel {
  readonly state: "loading" | "ready" | "error";
  readonly page: PurchasingAPAgingPage | null;
  readonly error: string;
  /** The day the books are read as of, as the operator picked it. */
  readonly asOf: string;
  /** The row whose open items are open, or nothing. */
  readonly openRowKey: string | null;
  /** The bill whose Pay form is open, or nothing. */
  readonly pay: PayBillForm | null;
  /** The role the desk is signed in as: it decides whether the Pay control is on the row. */
  readonly roleCode: string;
  /** True while a payment is on its way: the press waits, and the form says so. */
  readonly busy: boolean;
}

/**
 * The Pay form on one open vendor bill: the amount, the day it was paid, the note, and the key the
 * press carries. The key is minted when the form opens and is sent again on every retry, so a
 * double press pays once and a lost answer is retried under the same key.
 */
export interface PayBillForm {
  /** The bill's own id: the route's path carries it. */
  readonly billId: string;
  /** The bill's own number, so the form says which bill it is paying. */
  readonly billNo: string;
  /** What the bill has open, in the bill's own currency: the cap on the amount. */
  readonly openCents: number;
  /** The bill's currency. A bill that is not CAD is paid in it, at the rate the person types. */
  readonly currency: string;
  /** The amount as the person typed it, read back as money when the field is left. */
  readonly amountText: string;
  /** The day the payment was made: the house day by default. */
  readonly paidOn: string;
  /** The note, free text, for the file. */
  readonly note: string;
  /** The press's key: one per open form, sent again on every retry. */
  readonly key: string;
  /** What the server said, in words, when it refused. */
  readonly error: string;
  /**
   * The cents of an earlier payment the books now show landed on this bill after its answer was
   * lost, or null. While it is set the form sends nothing: it says the payment is on the bill and
   * waits for the person's own Close, so a press the screen had just invited never pays twice.
   */
  readonly landedCents?: number | null;
  /** An earlier payment on this bill the books showed landed after the form was closed, or null. */
  readonly earlierLandedCents?: number | null;
}

/** What the form says once the books show the earlier payment landed (S6 on 701, P2-3). */
export function payLandedWords(cents: number): string {
  return `The earlier payment of ${formatMoney(cents)} is on the bill. Nothing more to send.`;
}

/** What a fresh form says when an earlier payment on the bill landed after its form was closed. */
export function payEarlierLandedWords(cents: number): string {
  return `An earlier payment of ${formatMoney(cents)} on this bill is recorded. Record another only if it is a new payment.`;
}

/**
 * The roles that may pay a vendor bill: the money desk at Manage and above (the default role
 * configuration matrix, the money desk surface).
 *
 * The route is the core's own gate, a manage level route on the money desk, so the Payables and
 * Receivables role (at work on that surface) is turned away by it. The server re-refuses: this
 * only hides the control from a role the route would refuse, the same posture the desk takes
 * with the credit hold's lift.
 */
export function canPayBills(roleCode: string): boolean {
  return roleCode === "financial_controller" || roleCode === "general_manager";
}

/** True for a bill in a currency other than CAD: its Pay takes the amount in that currency and a rate. */
export function isForeignBill(currency: string | undefined): boolean {
  return currency !== undefined && currency !== "" && currency.toUpperCase() !== "CAD";
}

/**
 * What the bill has open in its own currency: the core's doc_open_cents for a bill that is not CAD,
 * the CAD open amount otherwise. This is the figure the Pay route caps the amount at.
 */
export function billOpenInOwnCurrency(item: Pick<PurchasingAPAgingItem, "open_cents" | "currency" | "doc_open_cents">): number {
  return isForeignBill(item.currency) && typeof item.doc_open_cents === "number" ? item.doc_open_cents : item.open_cents;
}

/** An amount in the bill's own currency: a CAD amount reads as before, another reads with its code. */
export function formatBillMoney(cents: number, currency: string): string {
  return isForeignBill(currency) ? `${formatMoney(cents)} ${currency.toUpperCase()}` : formatMoney(cents);
}

/** What the form says about the bill it is paying: which bill, to whom, and what is open on it. */
export function payTargetWords(billNo: string, payee: string, openCents: number, currency = "CAD"): string {
  return `Paying ${billNo} to ${payee}. The open amount is ${formatBillMoney(openCents, currency)}.`;
}

/** The words when the amount asked for is more than the bill has open. */
export function payOverOpenWords(openCents: number, currency = "CAD"): string {
  return `The amount cannot be more than the open amount, ${formatBillMoney(openCents, currency)}.`;
}

/** The words when a bill in another currency is paid without a usable rate. */
export function payRateWords(currency: string): string {
  return `Enter the rate the payment was made at, as dollars of CAD for one ${currency.toUpperCase()}, such as 1.3500.`;
}

/**
 * What was typed in the rate box: a positive decimal with at most six decimals, as the number the
 * route takes, or null. Nothing is computed from it here; the core converts and judges it.
 */
export function parseRate(text: string): number | null {
  const t = text.trim();
  if (!/^\d{1,6}(\.\d{1,6})?$/.test(t)) return null;
  const n = Number(t);
  return n > 0 ? n : null;
}

/** The words when the amount box holds nothing the desk can read as money. */
export function payAmountWords(): string {
  return "The amount must be a number of dollars, such as 250.00.";
}

/**
 * The Payables area's own views: the open AP board, and the T5018 year report (W3-T51). The area
 * stands beside the receivables desk in the Money Desk's area switch, so its own nav is the way
 * from one of its views to the other.
 */
export type PayablesView = "aging" | "t5018";

/**
 * The bands, in the desk's one vocabulary, with the contract's field on each. The word is the
 * screen's, the field is the route's, and the key is the core's own key for the band. The two
 * oldest bands carry the desk's aging signal: the signal is the class the Receivables aging tile
 * carries for the same age, so the two screens colour the same age the same way and the board
 * invents no colour of its own. The band name always rides with it, so the colour never carries
 * the meaning alone.
 */
export const PAYABLES_BANDS = [
  { key: "CURRENT", field: "current_cents", word: "Current", signal: "" },
  { key: "DAYS_30", field: "days_30_cents", word: "1 to 30 days", signal: "" },
  { key: "DAYS_60", field: "days_60_cents", word: "31 to 60 days", signal: "" },
  { key: "DAYS_60_TO_90", field: "days_60_to_90_cents", word: "61 to 90 days", signal: "tile-late" },
  { key: "DAYS_90_PLUS", field: "days_90_plus_cents", word: "Over 90 days", signal: "tile-escalation" },
] as const;

/** The words a landed cost line wears for what it was for: freight, duty or handling. */
export function landedCostWords(kind: string | undefined): readonly string[] {
  if (kind === "DUTY") return ["Duty"];
  if (kind === "HANDLING") return ["Handling"];
  if (kind === "FREIGHT") return ["Freight"];
  return [];
}

/**
 * The row's key: a vendor is keyed by the vendor's id, a landed cost by the landed cost's id.
 * The core stamps both on the row it sends, so the key is there for either kind of row.
 */
export function rowKey(row: PurchasingAPAgingRow): string {
  if (row.is_landed_cost) return row.landed_cost_id ?? row.vendor_id ?? "";
  return row.vendor_id ?? "";
}

/** The mark a row wears: a landed cost row says so, so the row is never read as a vendor. */
export function isLandedCostRow(row: PurchasingAPAgingRow): boolean {
  return row.is_landed_cost;
}

/** The items of a row, the oldest money first: a bill and a landed cost line read alike. */
export function rowItems(row: PurchasingAPAgingRow): readonly PurchasingAPAgingItem[] {
  return [...row.open_items].sort((a, b) => a.due_date.localeCompare(b.due_date) || a.ref_no.localeCompare(b.ref_no));
}
