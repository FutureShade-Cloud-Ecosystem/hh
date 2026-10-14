// Item 9.7: the Pay balance surface's words, pinned at the pure module. Every figure is the
// server's cents; money is formatted by the one helper, never printed as cents or a code.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  moneyTrailWords,
  paidInFullWords,
  takeButtonWords,
  payBalanceRefusalWords,
  payBalanceFindRefusalWords,
  readFindTruncated,
  readOwingOrders,
  MORE_ORDERS_WORDS,
  readTaken,
} from './words.ts';

const money = (c) => `$${(c / 100).toFixed(2)}`;
const wire = [
  { payment_id: 'p1', method: 'CARD', amount_cents: 50000 },
  { payment_id: 'p2', method: 'CASH', amount_cents: 1500 },
];
const payments = [
  { paymentId: 'p1', method: 'CARD', amountCents: 50000 },
  { paymentId: 'p2', method: 'CASH', amountCents: 1500 },
];

test('the money trail reads the whole answer, then the take', () => {
  assert.equal(moneyTrailWords(payments, money), 'Money trail: $500.00 by Card, then $15.00 by Cash.');
  assert.equal(moneyTrailWords([payments[0]], money), 'Money trail: $500.00 by Card.');
  assert.equal(moneyTrailWords([], money), '');
});

test('paid in full says released only when the answer says so', () => {
  assert.equal(paidInFullWords('DEMO-PLM-D01', 'Paid in full', true), 'DEMO-PLM-D01 reads Paid in full and is released to picking.');
  assert.equal(paidInFullWords('DEMO-PLM-D01', 'Paid in full', false), 'DEMO-PLM-D01 reads Paid in full.');
});

test('the button names the whole balance', () => {
  assert.equal(takeButtonWords(141359, (c) => '$1,413.59'), 'Take $1,413.59');
});

test('refusals read in words from the fields, never a code or cents', () => {
  assert.equal(
    payBalanceRefusalWords({ status: 422, error: 'pay_balance_whole_only', message: 'This door takes the whole balance of $14.13.' }, money),
    'This door takes the whole balance of $14.13.',
  );
  assert.match(payBalanceRefusalWords({ status: 422, error: 'pay_balance_whole_only', balance_cents: 1413, sent_cents: 100 }, money), /\$14\.13/);
  assert.equal(payBalanceRefusalWords({ status: 409, error: 'The order is paid in full.' }, money), 'The order is paid in full.');
  assert.match(payBalanceRefusalWords({ status: 0, error: 'Failed to fetch' }, money), /cannot be charged twice/);
  assert.match(payBalanceRefusalWords({ status: 404, error: 'no such order' }, money), /no longer answers/);
  assert.doesNotMatch(payBalanceRefusalWords({ status: 402, error: 'card_charge_card_declined' }, money), /card_charge/);
});

// Item 9 visual review P2-1: a refused find speaks in words. No answer is one retry line; any
// other refusal is the server's message when the capture has one, else one plain sentence; never
// the envelope's raw error string or code.
test('a refused find speaks in words, never the raw error string', () => {
  assert.equal(payBalanceFindRefusalWords({ status: 0, error: 'Failed to fetch' }), 'No answer from the server. Try again.');
  assert.equal(
    payBalanceFindRefusalWords({ status: 422, error: 'staff_auth_required', message: 'Sign in again to search.' }),
    'Sign in again to search.',
  );
  const plain = payBalanceFindRefusalWords({ status: 403, error: 'branch_mismatch' });
  assert.equal(plain, 'The search was refused: try again, or ask a manager.');
  assert.doesNotMatch(plain, /branch_mismatch/);
});

test('the find answer is read whole, an unreadable row is dropped', () => {
  const row = {
    order_id: 'o1', order_no: 'DEMO-PLM-D01', customer_id: 'c1', customer_name: 'Ridgeview', fulfilment: 'WILL_CALL',
    order_total_cents: 100, paid_cents: 40, still_owed_cents: 60, held_from_picking: true, hold_words: 'held', payments: wire,
    job: 'Marina Road patio',
  };
  const read = readOwingOrders({ items: [row, { order_no: 'x' }, null] });
  assert.equal(read.length, 1);
  assert.equal(read[0].orderNo, 'DEMO-PLM-D01');
  assert.equal(read[0].stillOwedCents, 60);
  assert.equal(read[0].job, 'Marina Road patio');
  assert.deepEqual(readOwingOrders(null), []);
});

test('the take answer is read whole or not at all', () => {
  const answer = {
    order_id: 'o1', order_no: 'DEMO-PLM-D01', customer_id: 'c1', payment_id: 'p3', method: 'CARD', amount_cents: 60,
    paid_words: 'Paid in full', released_to_picking: true, still_owed_cents: 0, held_from_picking_now: false, payments: wire, replayed: true,
  };
  assert.equal(readTaken(answer).orderNo, 'DEMO-PLM-D01');
  assert.equal(readTaken(answer).replayed, true);
  assert.equal(readTaken({ ...answer, still_owed_cents: '0' }), null);
  assert.equal(readTaken(null), null);
});

test('a find the server cut short reads as truncated, only on a true', () => {
  assert.equal(readFindTruncated({ items: [], truncated: true }), true);
  assert.equal(readFindTruncated({ items: [], truncated: false }), false);
  assert.equal(readFindTruncated({ items: [], truncated: 'true' }), false);
  assert.equal(readFindTruncated(null), false);
  assert.equal(MORE_ORDERS_WORDS, 'More orders match: search by order number.');
});
