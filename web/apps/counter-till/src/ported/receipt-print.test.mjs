// Ported from hardscapeos_dibbits master 87d7941b app/src/receipt-print.test.mjs; adapted for HH.

// Regression for the DIB-410 receipt print flow (receipt-print.ts). Node built-in test runner,.ts
// source through the loader: node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs'
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { receiptPath, receiptErrorMessage, printReceiptInto } from './receipt-print-logic.ts';

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
function fakeWindow() {
  const calls = { written: '', closed: false, focused: false, printed: false };
  return {
    calls,
    document: {
      write(html) {
        calls.written += html;
      },
      close() {
        calls.closed = true;
      },
    },
    focus() {
      calls.focused = true;
    },
    print() {
      calls.printed = true;
    },
  };
}

test('DIB-410: the fetched document is WRITTEN into the window and printed (never navigated to)', () => {
 // Navigating the new window to the route would drop the Bearer token and render a 401 at the
 // customer: so the flow writes the already-authenticated bytes instead.
  const win = fakeWindow();
  const html = '<!doctype html><html><body>#SO-42</body></html>';
  const out = printReceiptInto(win, html);
  assert.equal(out.ok, true);
  assert.equal(win.calls.written, html);
  assert.equal(win.calls.closed, true, 'the document stream must be closed before printing');
  assert.equal(win.calls.printed, true, 'the print dialog is the whole point of the flow');
});

test('DIB-410: a blocked pop-up is reported to the operator, never a silent no-op', () => {
  const out = printReceiptInto(null, '<html></html>');
  assert.equal(out.ok, false);
  assert.equal(out.reason, 'popup_blocked');
  assert.match(out.message, /pop-ups/i);
});
