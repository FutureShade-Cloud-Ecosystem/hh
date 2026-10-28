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
}

/**
 * The bands, in the desk's one vocabulary, with the contract's field on each. The word is the
 * screen's, the field is the route's, and the key is the core's own key for the band.
 */
export const PAYABLES_BANDS = [
  { key: "CURRENT", field: "current_cents", word: "Current" },
  { key: "DAYS_30", field: "days_30_cents", word: "1 to 30 days" },
  { key: "DAYS_60", field: "days_60_cents", word: "31 to 60 days" },
  { key: "DAYS_60_TO_90", field: "days_60_to_90_cents", word: "61 to 90 days" },
  { key: "DAYS_90_PLUS", field: "days_90_plus_cents", word: "Over 90 days" },
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
