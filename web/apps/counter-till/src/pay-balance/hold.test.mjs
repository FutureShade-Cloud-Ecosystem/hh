// Item 9.8: the held order card's words and the hold slip, pinned at the pure module.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HOLD_PANEL_LINE, HOLD_TITLE_FALLBACK, readPayHold, holdSlipHtml } from './hold.ts';

const money = (c) => `$${(c / 100).toFixed(2)}`;

test('the hold line is the board sentence, word for word', () => {
  assert.equal(
    HOLD_PANEL_LINE,
    'Cash On Demand means pay before release, never cash on delivery. The deposit put it in process; the hold lifts on its own when the last dollar lands.',
  );
  assert.equal(HOLD_TITLE_FALLBACK, 'This order cannot reach picking until the balance is paid.');
});

test('the order read answers pay_hold; anything not whole is not a hold', () => {
  const hold = readPayHold({
    held_from_picking: true,
    words: 'This order cannot reach picking until the balance is paid.',
    in_process_words: 'In process: deposit taken',
    order_total_cents: 6102,
    paid_cents: 2000,
    owed_cents: 4102,
  });
  assert.equal(hold.held, true);
  assert.equal(hold.words, 'This order cannot reach picking until the balance is paid.');
  assert.equal(hold.inProcessWords, 'In process: deposit taken');
  assert.equal(hold.owedCents, 4102);
  assert.equal(readPayHold({ held_from_picking: false, order_total_cents: 1, paid_cents: 1, owed_cents: 0 }).held, false);
  assert.equal(readPayHold({ held_from_picking: true, order_total_cents: '1' }), null);
  assert.equal(readPayHold(null), null);
  assert.equal(readPayHold(undefined), null);
});

test('the hold slip carries the order number, customer, balance due and the hold sentence, escaped', () => {
  const slip = holdSlipHtml(
    { orderNo: 'SO-TRT-18', customerName: 'Fenwick <b>&</b> Sons', balanceDueCents: 4102, holdWords: 'This order cannot reach picking until the balance is paid.' },
    money,
  );
  assert.match(slip, /SO-TRT-18/);
  assert.match(slip, /\$41\.02/);
  assert.match(slip, /This order cannot reach picking until the balance is paid\./);
  assert.match(slip, /Fenwick &lt;b&gt;&amp;&lt;\/b&gt; Sons/);
  assert.doesNotMatch(slip, /<b>/);
  assert.doesNotMatch(slip, /4102/);
});
