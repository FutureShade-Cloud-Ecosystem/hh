// The Payables board's own vocabulary: the shape the purchasing route answers until
// W2-9a's contract lands on main, the aging bands the board shows in the desk's one
// vocabulary, and the words a landed cost line wears.
//
// Money is the server's: the board reads the figures the route sends and never adds them
// up, the same rule the receivables slice holds (test/no-app-money.test.mjs).
//
// The bands are the C8 five: the same bands the desk's own account workspace draws and the
// same words ar-logic.ts reads them back in. When W2-9a's contract is regenerated on main,
// this file is the one place the shape changes: the generated type replaces the shapes
// below and PAYABLES_BANDS keeps the screen's column order.

/** The five bands every payee and the whole book are read in. */
export type PayablesBoardBands = {
  readonly current_cents: number;
  readonly days_30_cents: number;
  readonly days_60_cents: number;
  readonly days_90_cents: number;
  readonly days_90_plus_cents: number;
};

/** One open document on a payee's row: a bill, or a landed cost line to a carrier or broker. */
export type PayablesBoardLine = {
  readonly doc_no: string;
  readonly doc_kind: "BILL" | "LANDED_COST";
  /** What the landed cost was for: freight, duty or handling. Absent on a bill. */
  readonly landed_cost_kind?: "FREIGHT" | "DUTY" | "HANDLING";
  readonly due_date: string;
  readonly open_cents: number;
  /** What has been paid against it, so a part paid bill says so. */
  readonly paid_cents: number;
  readonly days_overdue: number;
};

/** One payee's open AP: the bands, the total, and the documents behind them. */
export type PayablesBoardRow = PayablesBoardBands & {
  readonly payee_id: string;
  readonly payee_name: string;
  /** A vendor, or the carrier or broker a landed cost is owed to. */
  readonly payee_kind: "VENDOR" | "LANDED_COST";
  readonly payment_terms_days: number;
  readonly open_cents: number;
  readonly lines: readonly PayablesBoardLine[];
};

/** The board's read: the payees, the bands of the whole book, and the day it is read as of. */
export type PayablesBoardPage = {
  readonly as_of: string;
  readonly items: readonly PayablesBoardRow[];
  readonly bands: PayablesBoardBands;
  readonly total_cents: number;
};

/** The board's own state: the read, the day it is read as of, and the payee open. */
export interface PayablesBoardModel {
  readonly state: "loading" | "ready" | "error";
  readonly page: PayablesBoardPage | null;
  readonly error: string;
  /** The day the books are read as of, as the operator picked it. */
  readonly asOf: string;
  /** The payee whose open items are open, or nothing. */
  readonly openPayeeId: string | null;
}

/**
 * The bands, in the desk's one vocabulary. The key is the core's own band key, the word is
 * the screen's, and the field is where the figure sits on the row.
 */
export const PAYABLES_BANDS = [
  { key: "CURRENT", field: "current_cents", word: "Current" },
  { key: "1_30", field: "days_30_cents", word: "1 to 30 days" },
  { key: "31_60", field: "days_60_cents", word: "31 to 60 days" },
  { key: "61_90", field: "days_90_cents", word: "61 to 90 days" },
  { key: "90_PLUS", field: "days_90_plus_cents", word: "Over 90 days" },
] as const;

/** The words a landed cost line wears for what it was for: freight, duty or handling. */
export function landedCostWords(kind: string | undefined): readonly string[] {
  if (kind === "DUTY") return ["Duty"];
  if (kind === "HANDLING") return ["Handling"];
  if (kind === "FREIGHT") return ["Freight"];
  return [];
}

/** The mark a payee row wears: a landed cost payee says so, so the row is never read as a vendor. */
export function isLandedCostPayee(row: PayablesBoardRow): boolean {
  return row.payee_kind === "LANDED_COST";
}

/** The lines of a payee, the oldest money first: a bill and a landed cost line read alike. */
export function payeeLines(row: PayablesBoardRow): readonly PayablesBoardLine[] {
  return [...row.lines].sort((a, b) => a.due_date.localeCompare(b.due_date) || a.doc_no.localeCompare(b.doc_no));
}
