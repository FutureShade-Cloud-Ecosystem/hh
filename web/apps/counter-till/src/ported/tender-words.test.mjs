// Ported from hardscapeos_dibbits master 87d7941b app/src/tender-words.test.mjs; adapted for HH.

// Item 3b, group "tender": the word maps behind the five money surfaces (payment-history.ts,
// pos-lookup.ts, till-close.ts, the AR console, pos-assess-relief.ts) and the one rule that makes
// them safe to keep: the DEFAULT ARM DEGRADES HONESTLY.
//
// The two schema lists below are copied from the migrations, deliberately, rather than derived from
// the maps under test. A test that read Object.keys(MAP) would pass the day someone dropped a value
// out of the map: the point is that the map and the CHECK constraint have to be told about each
// other, and this file is where a schema widening that skipped the UI shows up.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PAYMENT_STATUS_LABEL, TENDER_METHOD_LABEL, paymentStatusWord, tenderMethodWord } from './tender-logic.ts';
// The other two words my five surfaces render already have homes, and this file does not second-guess
// them: it only pins that the arm my call sites lean on is there. orders.status belongs to the order
// module's matrix (order-logic.ts, covered by order-logic.test.mjs) and tax_decision_snapshot
//.tax_reason_code to the tax module's api file (tax-api.ts, covered by ledger-words.test.mjs), which is
// the warranty-api.ts precedent. pos-lookup.ts and pos-assess-relief.ts import from those rather than
// keeping a second copy of the same fact.
import { orderStatusWord } from './order-logic.ts';
import { taxReasonWord } from './tax-api.ts';

// ── the tender method: payments.method (0010) ∪ till_tender_totals.method (0028) ∪
// pos_sale.tender (0029) ∪ pos_sale_tenders.method (0100) ────────────────────────────────────

const SCHEMA_METHODS = ['ACCOUNT', 'CASH', 'CARD', 'CHEQUE', 'EFT', 'OTHER'];

test('every tender the four CHECK constraints allow has a word', () => {
  for (const m of SCHEMA_METHODS) {
    assert.ok(TENDER_METHOD_LABEL[m], `${m} (a stored tender) has no word`);
  }
  assert.equal(Object.keys(TENDER_METHOD_LABEL).length, SCHEMA_METHODS.length);
});

test('each tender renders the counter’s own word for it', () => {
  assert.equal(tenderMethodWord('ACCOUNT'), 'Account');
  assert.equal(tenderMethodWord('CASH'), 'Cash');
  assert.equal(tenderMethodWord('CARD'), 'Card');
  assert.equal(tenderMethodWord('CHEQUE'), 'Cheque');
  assert.equal(tenderMethodWord('OTHER'), 'Other');
});

test('EFT keeps its capitals: it is the trade’s word, and the AR payment control already says it', () => {
  assert.equal(tenderMethodWord('EFT'), 'EFT');
});

test('an unmapped tender degrades HONESTLY: itself, lowercased, never Unknown and never empty', () => {
  assert.equal(tenderMethodWord('STORE_CREDIT'), 'store credit');
  assert.equal(tenderMethodWord('INTERAC'), 'interac');
  assert.notEqual(tenderMethodWord('STORE_CREDIT'), 'Unknown');
  assert.notEqual(tenderMethodWord('STORE_CREDIT'), '');
});

// ── the payment's own state: payments.status (0010, widened by 0027) ──────────────────────────────

const SCHEMA_PAYMENT_STATUSES = ['RECEIVED', 'DEPOSITED', 'BOUNCED', 'REFUNDED', 'VOID', 'PENDING'];

test('every payments.status the 0027 CHECK allows has a word', () => {
  for (const s of SCHEMA_PAYMENT_STATUSES) {
    assert.ok(PAYMENT_STATUS_LABEL[s], `${s} (a stored payment status) has no word`);
  }
  assert.equal(Object.keys(PAYMENT_STATUS_LABEL).length, SCHEMA_PAYMENT_STATUSES.length);
});

test('a payment state renders as a person would say it, VOID as the verb that was done to it', () => {
  assert.equal(paymentStatusWord('RECEIVED'), 'Received');
  assert.equal(paymentStatusWord('DEPOSITED'), 'Deposited');
  assert.equal(paymentStatusWord('BOUNCED'), 'Bounced');
  assert.equal(paymentStatusWord('REFUNDED'), 'Refunded');
  assert.equal(paymentStatusWord('VOID'), 'Voided');
  assert.equal(paymentStatusWord('PENDING'), 'Pending');
});

test('an unmapped payment status degrades HONESTLY', () => {
  assert.equal(paymentStatusWord('CHARGED_BACK'), 'charged back');
  assert.notEqual(paymentStatusWord('CHARGED_BACK'), 'Unknown');
  assert.notEqual(paymentStatusWord('CHARGED_BACK'), '');
});

// ── the shape itself: no map may answer with silence ──────────────────────────────────────────────

test('no word map hides an unmapped value behind an empty string, in any of the four', () => {
  for (const fn of [tenderMethodWord, paymentStatusWord, orderStatusWord, taxReasonWord]) {
    assert.equal(fn('SOME_NEW_STATE'), 'some new state');
  }
});
