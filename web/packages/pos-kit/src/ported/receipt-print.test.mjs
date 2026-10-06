// Ported from hardscapeos_dibbits master 87d7941b app/src/receipt-print.test.mjs; adapted for HH.

// Regression for the DIB-410 receipt print flow (receipt-print.ts). Node built-in test runner,.ts
// source through the loader: node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs'
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { receiptPath, receiptErrorMessage } from './receipt-print-logic.ts';

// ── the route ────────────────────────────────────────────────────────────────
test('DIB-410: the receipt route hangs off the sale, and the id is encoded', () => {
  assert.equal(
    receiptPath('9f1c0a2e-0000-4000-9000-000000000001'),
    '/api/v1/pos/sales/9f1c0a2e-0000-4000-9000-000000000001/receipt',
  );
 // An operator-typed value must never break out of the path segment.
  assert.equal(receiptPath('a/b?c=1'), '/api/v1/pos/sales/a%2Fb%3Fc%3D1/receipt');
});

// ── the refusals a cashier has to be able to act on ─────────────────────────
test('DIB-410: a 403 says the sale is another branch’s, not "something went wrong"', () => {
  const msg = receiptErrorMessage(403);
  assert.match(msg, /another branch/i);
  assert.doesNotMatch(msg, /^Could not load/);
});

test('DIB-410: 404 and 401 each say what actually happened; anything else names the status', () => {
  assert.match(receiptErrorMessage(404), /No committed sale/i);
  assert.match(receiptErrorMessage(401), /sign in again/i);
  assert.equal(receiptErrorMessage(500), 'Could not load the receipt (500).');
});

// ── the print flow ──────────────────────────────────────────────────────────
// HH note: the two choreography cases here (the fetched document written into the window and
// printed, and the pop-up-blocked refusal) drove printReceiptInto, which is a host seam now
// (ports.ts, named in CLOSURE.md); they stayed with the reference until the wiring item
// implements the seam, and its implementations bring the cases back against it.

// ── the format parameter (row 666 item 2) ───────────────────────────────────
// The route gained one optional query value: the paper. The roll is the default and builds the
// same URL the buttons have always built, the page names the full-page document, and the id is
// encoded on both.
test('the receipt route builds the roll URL by default, and format=page names the full-page paper', () => {
  const id = '9f1c0a2e-0000-4000-9000-000000000001';
  // The default and the explicit roll are the same bytes the route has always served.
  assert.equal(receiptPath(id), '/api/v1/pos/sales/9f1c0a2e-0000-4000-9000-000000000001/receipt');
  assert.equal(
    receiptPath(id, 'roll'),
    '/api/v1/pos/sales/9f1c0a2e-0000-4000-9000-000000000001/receipt',
  );
  assert.equal(
    receiptPath(id, 'page'),
    '/api/v1/pos/sales/9f1c0a2e-0000-4000-9000-000000000001/receipt?format=page',
  );
  // An operator-typed value must never break out of the path segment, whichever paper is asked.
  assert.equal(receiptPath('a/b?c=1', 'page'), '/api/v1/pos/sales/a%2Fb%3Fc%3D1/receipt?format=page');
});
