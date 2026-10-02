// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-split.test.mjs and the
// money halves of app/src/_pos.test.mjs; adapted for HH.

// The host seams the wiring item owes tests for (CLOSURE.md, "Left out, with
// reasons"): the split tender arithmetic, the typed-amount parses and the
// ticket assemblies now live in ports.ts, so their cases run against the
// implementations rather than against the seams' refusals. Node built-in
// runner, same loader as the other ported suites.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MAX_SPLIT_LEGS,
  autoFillRemainder,
  availableCreditCents,
  balanceAfterDepositCents,
  cartSubtotalCents,
  cashTenderedPlan,
  depositCentsFromTyped,
  evaluateSplit,
  parseAmountCents,
  refundEstimateCents,
  sumLineSavingCents,
  ticketTaxCents,
  ticketTotalCents,
} from './ports.ts';

const TOTAL = 11300; // CA$113.00, the sale being settled in most cases below

// ── the exact-sum rule ───────────────────────────────────────────────────────
test('an exactly-settled split is ready to ring', () => {
  const s = evaluateSplit(
    [
      { method: 'CASH', amountCents: 4000 },
      { method: 'CARD', amountCents: 7300 },
    ],
    TOTAL,
    false,
  );
  assert.equal(s.tenderedCents, TOTAL);
  assert.equal(s.remainingCents, 0);
  assert.equal(s.ready, true);
  assert.equal(s.problem, '');
});

test('one cent short is not ready, and says how much is left', () => {
  const s = evaluateSplit(
    [
      { method: 'CASH', amountCents: 4000 },
      { method: 'CARD', amountCents: 7299 },
    ],
    TOTAL,
    false,
  );
  assert.equal(s.ready, false);
  assert.equal(s.remainingCents, 1);
  assert.match(s.problem, /CA\$0\.01 still to tender/);
});

test('one cent over is not ready, and says by how much', () => {
  const s = evaluateSplit(
    [
      { method: 'CASH', amountCents: 4000 },
      { method: 'CARD', amountCents: 7301 },
    ],
    TOTAL,
    false,
  );
  assert.equal(s.ready, false);
  assert.equal(s.remainingCents, -1);
  assert.match(s.problem, /CA\$0\.01 over-tendered/);
});

// ── shape rules, mirroring the server so the cashier never learns them from a 4xx ────────────
test('a single leg is not a split', () => {
  const s = evaluateSplit([{ method: 'CASH', amountCents: TOTAL }], TOTAL, false);
  assert.equal(s.ready, false);
  assert.match(s.problem, /at least two/);
});

test('a blank amount is refused rather than treated as zero', () => {
  const s = evaluateSplit(
    [
      { method: 'CASH', amountCents: parseAmountCents('') },
      { method: 'CARD', amountCents: TOTAL },
    ],
    TOTAL,
    false,
  );
  assert.equal(s.ready, false);
  assert.match(s.problem, /amount greater than zero/);
});

test('two account legs are refused (the server would post the remainder to AR twice)', () => {
  const s = evaluateSplit(
    [
      { method: 'ACCOUNT', amountCents: 4000 },
      { method: 'ACCOUNT', amountCents: 4000 },
      { method: 'CASH', amountCents: 3300 },
    ],
    TOTAL,
    true,
  );
  assert.equal(s.ready, false);
  assert.match(s.problem, /Only one tender can go on account/);
});

test('an account leg needs a customer, asked for BEFORE the cash leg is collected', () => {
  const legs = [
    { method: 'CASH', amountCents: 3300 },
    { method: 'ACCOUNT', amountCents: 8000 },
  ];
  assert.match(evaluateSplit(legs, TOTAL, false).problem, /Pick a customer account/);
  // With an account selected the same split is ready.
  assert.equal(evaluateSplit(legs, TOTAL, true).ready, true);
});

test('an empty cart cannot be split', () => {
  const s = evaluateSplit(
    [
      { method: 'CASH', amountCents: 100 },
      { method: 'CARD', amountCents: 100 },
    ],
    0,
    false,
  );
  assert.equal(s.ready, false);
  assert.match(s.problem, /Add a line/);
});

test('the leg count is bounded at the same place as the server', () => {
  const legs = Array.from({ length: MAX_SPLIT_LEGS + 1 }, () => ({ method: 'CASH', amountCents: 100 }));
  assert.match(evaluateSplit(legs, TOTAL, false).problem, new RegExp(`at most ${MAX_SPLIT_LEGS}`));
});

// ── the ergonomics that stops the off-by-a-cent happening at all ─────────────────────────────
test('autoFillRemainder pre-fills exactly what is left', () => {
  const legs = [
    { method: 'CASH', amountCents: 4000 },
    { method: 'CARD', amountCents: 0 },
  ];
  assert.equal(autoFillRemainder(legs, TOTAL, 1), 7300);
  // And filling it makes the split ready: the arithmetic cannot be off by construction.
  legs[1].amountCents = autoFillRemainder(legs, TOTAL, 1);
  assert.equal(evaluateSplit(legs, TOTAL, false).ready, true);
});

test('autoFillRemainder never pre-fills a negative (an already-covered sale)', () => {
  const legs = [
    { method: 'CASH', amountCents: TOTAL + 500 },
    { method: 'CARD', amountCents: 0 },
  ];
  assert.equal(autoFillRemainder(legs, TOTAL, 1), 0);
});

// ── parsing what a human types ───────────────────────────────────────────────
test('parseAmountCents handles the shapes people actually type', () => {
  assert.equal(parseAmountCents('40'), 4000);
  assert.equal(parseAmountCents('40.00'), 4000);
  assert.equal(parseAmountCents('$40.00'), 4000);
  assert.equal(parseAmountCents('1,234.56'), 123456);
  assert.equal(parseAmountCents(' 73 '), 7300);
  // 0.1 + 0.2 float arithmetic must not lose a cent on the way in.
  assert.equal(parseAmountCents('0.29'), 29);
  assert.equal(parseAmountCents('113.00'), 11300);
});

test('parseAmountCents returns NaN for a blank or non-numeric entry, never 0', () => {
  for (const raw of ['', '   ', 'abc', '4o.00', '--1']) {
    assert.equal(Number.isNaN(parseAmountCents(raw)), true, `expected NaN for ${JSON.stringify(raw)}`);
  }
});

test('a third decimal is refused, never rounded (the app parser carries the same rule)', () => {
  assert.equal(Number.isNaN(parseAmountCents('20.005')), true);
});

// ── the deposit parse and the cash quick entry's plan ────────────────────────
test('depositCentsFromTyped answers 0 for a refused entry, the typed cents otherwise', () => {
  assert.equal(depositCentsFromTyped('40.55'), 4055);
  assert.equal(depositCentsFromTyped('200'), 20000);
  assert.equal(depositCentsFromTyped(''), 0);
  assert.equal(depositCentsFromTyped('abc'), 0);
});

test('cashTenderedPlan answers the change due only when the entry covers the total', () => {
  const covering = cashTenderedPlan(TOTAL, '120');
  assert.equal(covering.tenderedCents, 12000);
  assert.equal(covering.sendable, true);
  assert.equal(covering.changeDueCents, 700);
  // A short entry sends nothing: the ring then takes the exact total.
  const short = cashTenderedPlan(TOTAL, '100');
  assert.equal(short.sendable, false);
  assert.equal(short.changeDueCents, 0);
  // An empty or refused box is the same no-entry posture.
  for (const raw of ['', 'abc']) {
    const plan = cashTenderedPlan(TOTAL, raw);
    assert.equal(plan.tenderedCents, 0);
    assert.equal(plan.sendable, false);
    assert.equal(plan.changeDueCents, 0);
  }
  // Nothing covers an empty ticket.
  assert.equal(cashTenderedPlan(0, '5').sendable, false);
});

// ── the ticket assemblies, over core-priced figures ──────────────────────────
test('cartSubtotalCents sums the priced lines and skips nothing into a guess', () => {
  assert.equal(cartSubtotalCents([{ lineTotalCents: 4000 }, { lineTotalCents: 7300 }]), 11300);
  assert.equal(cartSubtotalCents([]), 0);
  assert.equal(
    cartSubtotalCents([{ lineTotalCents: 4000 }, { lineTotalCents: NaN }, { lineTotalCents: 7300 }]),
    11300,
  );
});

test('ticketTaxCents and ticketTotalCents assemble the ticket over the stated rate', () => {
  assert.equal(ticketTaxCents(10000, 1300), 1300);
  assert.equal(ticketTaxCents(1965, 1300), 255); // rounded to the whole cent
  assert.equal(ticketTotalCents(10000, 1300), 11300);
});

test('sumLineSavingCents multiplies per line and sums, skipping what the engine left out', () => {
  assert.equal(sumLineSavingCents([{ saving_cents: 72, qty: 3 }, { saving_cents: 140, qty: 2 }]), 496);
  assert.equal(sumLineSavingCents([{ saving_cents: NaN, qty: 3 }, { saving_cents: 140, qty: 2 }]), 280);
  assert.equal(sumLineSavingCents([]), 0);
});

test('balanceAfterDepositCents answers the balance, never below zero', () => {
  assert.equal(balanceAfterDepositCents(TOTAL, 5000), 6300);
  assert.equal(balanceAfterDepositCents(100, 500), 0);
});

// ── the refund estimate and the credit headroom, over the lookup's own figures ───────────────
test('refundEstimateCents prorates the picked quantities over the sold ones', () => {
  const lines = [
    { order_line_id: 'a', unit_price_cents: 6000, tax_cents: 0, qty_sold: 2 },
    { order_line_id: 'b', unit_price_cents: 6000, tax_cents: 780, qty_sold: 2 },
  ];
  // One of two on the first line: half its total, no tax in this world.
  const half = refundEstimateCents([lines[0]], { a: 1 });
  assert.deepEqual(half, { subtotal: 3000, tax: 0, total: 3000 });
  // The whole of the second line: its own total and its own tax back.
  const whole = refundEstimateCents([lines[1]], { b: 2 });
  assert.deepEqual(whole, { subtotal: 6000, tax: 780, total: 6780 });
  // An unpicked line contributes nothing.
  const none = refundEstimateCents(lines, {});
  assert.deepEqual(none, { subtotal: 0, tax: 0, total: 0 });
});

test('availableCreditCents clamps the headroom at zero, never a negative offer', () => {
  assert.equal(availableCreditCents({ credit_limit_cents: 500000, open_balance_cents: 125000 }), 375000);
  assert.equal(availableCreditCents({ credit_limit_cents: 100000, open_balance_cents: 125000 }), 0);
  assert.equal(availableCreditCents({ credit_limit_cents: NaN, open_balance_cents: 0 }), 0);
});
