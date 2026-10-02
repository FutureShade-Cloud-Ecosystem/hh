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
  type ReceiptFormat,
  type ReceiptWindow,
} from './receipt-print-logic.js';

export {
  printReceiptInto,
  receiptErrorMessage,
  receiptPath,
  type PrintOutcome,
  type ReceiptFormat,
  type ReceiptWindow,
} from './receipt-print-logic.js';

/** Fetch the server-rendered receipt document (staff-gated; the token rides on api()). */
export async function fetchReceiptHTML(saleId: string, format: ReceiptFormat = 'roll'): Promise<string> {
  const res = await api(receiptPath(saleId, format));
  if (!res.ok) throw new Error(receiptErrorMessage(res.status));
  return res.text();
}

/**
 * The whole flow: fetch the document with the session token, open a window, print. `openWindow` is
 * injected so the flow can be exercised without a browser. The format picks the paper: the 80 mm
 * roll (the default) or the full-page document.
 */
export async function printReceipt(
  saleId: string,
  openWindow: () => ReceiptWindow | null = openBlankWindow,
  format: ReceiptFormat = 'roll',
): Promise<PrintOutcome> {
  const receiptHtml = await fetchReceiptHTML(saleId, format);
  return printReceiptInto(openWindow(), receiptHtml);
}

// HH note: the reference opened the blank window by hand here; the host owns the window
// surgery around printing, so the opener is a seam (ports.ts, named in CLOSURE.md), same
// signature, same injected shape in printReceipt.
import { openBlankWindow } from './ports.js';
export { openBlankWindow };
