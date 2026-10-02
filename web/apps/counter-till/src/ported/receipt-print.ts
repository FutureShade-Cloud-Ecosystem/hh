// Ported from hardscapeos_dibbits master 87d7941b app/src/receipt-print.ts; adapted for HH.

// receipt-print.ts: DIB-410 counter receipt: the network half of the print flow.
//
// WHY THE HTML IS FETCHED AND NOT NAVIGATED TO. The receipt route is staff-gated and the staff
// session is a Bearer token held in the browser's session store (the host's): `window.open('/api/v1/pos/sales/…')`
// sends no Authorization header, so it would render a 401 in the customer-facing window. So the
// document is FETCHED with the token, then written into a blank window that is printed. Same result,
// and it keeps the auth model intact.
//
// The pure parts (route, refusal messages, the window write/print) live in receipt-print-logic.ts
// and are unit-tested there.

import { api } from './ports.js';
import {
  printReceiptInto,
  receiptErrorMessage,
  receiptPath,
  type PrintOutcome,
  type ReceiptWindow,
} from './receipt-print-logic.js';

export {
  printReceiptInto,
  receiptErrorMessage,
  receiptPath,
  type PrintOutcome,
  type ReceiptWindow,
} from './receipt-print-logic.js';

/** Fetch the server-rendered receipt document (staff-gated; the token rides on api()). */
export async function fetchReceiptHTML(saleId: string): Promise<string> {
  const res = await api(receiptPath(saleId));
  if (!res.ok) throw new Error(receiptErrorMessage(res.status));
  return res.text();
}

/**
 * The whole flow: fetch the document with the session token, open a window, print. `openWindow` is
 * injected so the flow can be exercised without a browser.
 */
export async function printReceipt(
  saleId: string,
  openWindow: () => ReceiptWindow | null = openBlankWindow,
): Promise<PrintOutcome> {
  const receiptHtml = await fetchReceiptHTML(saleId);
  return printReceiptInto(openWindow(), receiptHtml);
}

/** The default opener: a blank same-origin window the fetched document is navigated into. */
export function openBlankWindow(): ReceiptWindow | null {
  if (typeof window === 'undefined') return null;
  return window.open('', '_blank') as unknown as ReceiptWindow | null;
}
