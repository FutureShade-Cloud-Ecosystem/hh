// Ported from hardscapeos_dibbits master 87d7941b app/src/receipt-print-logic.ts; adapted for HH.

// receipt-print-logic.ts: the pure, DOM-free half of the DIB-410 receipt print flow.
//
// Split out from receipt-print.ts so it can be unit-tested on the Node runner: the api.ts helper
// reads the session store at module load, so anything importing it cannot be imported by a DOM-free
// test. Same reason _pos.ts exists.

/** The receipt route for one sale (accepts the pos_sale id OR the order id: the server resolves both). */
export function receiptPath(saleId: string): string {
  return `/api/v1/pos/sales/${encodeURIComponent(saleId)}/receipt`;
}

/**
 * The operator-facing message for a failed receipt fetch. The 403 case is NOT a generic error: the
 * server refuses a receipt whose sale belongs to a branch this caller cannot reach, and a cashier
 * needs to be told that rather than left retrying.
 */
export function receiptErrorMessage(status: number): string {
  if (status === 403) return 'That sale belongs to another branch: its receipt is not available here.';
  if (status === 404) return 'No committed sale found for that number.';
  if (status === 401) return 'Session expired: sign in again to print.';
  return `Could not load the receipt (${status}).`;
}

/** The subset of a browser window this flow touches: injectable, so the flow is testable. */
export interface ReceiptWindow {
  document: { write(markup: string): void; close(): void };
  focus?(): void;
  print?(): void;
}

export type PrintOutcome = { ok: true } | { ok: false; reason: 'popup_blocked'; message: string };

// HH note: printReceiptInto wrote the fetched document into the fresh window with the browser's
// document writer and called print. This app's fence on raw markup paths reserves that write for
// the host (and a shell prints through its own chrome), so the choreography is a host seam with
// the reference's signature: ports.ts, named in CLOSURE.md. The pop-up-blocked case keeps its
// operator words there.
export { printReceiptInto } from './ports.js';
