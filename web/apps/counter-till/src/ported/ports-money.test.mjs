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
  perLineSavingCents,
  availableCreditCents,
  balanceAfterDepositCents,
  cartSubtotalCents,
  cashTenderedPlan,
  depositCentsFromTyped,
  evaluateSplit,
  parseAmountCents,
  refundEstimateCents,
  splitDraftRead,
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
  // The decimal string answers 29 exactly, never a rounded 28.999...; the
  // case that actually bites a float multiply is the third decimal below.
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
  // The change preview's own figures, on a worked ticket: a CA$72.32 total
  // tendered CA$80.00 shows CA$7.68; the exact total shows none; a cent short
  // is not sendable and promises nothing.
  assert.deepEqual(cashTenderedPlan(7232, '80.00'), { tenderedCents: 8000, changeDueCents: 768, sendable: true });
  assert.deepEqual(cashTenderedPlan(7232, '72.32'), { tenderedCents: 7232, changeDueCents: 0, sendable: true });
  assert.deepEqual(cashTenderedPlan(7232, '72.31'), { tenderedCents: 7231, changeDueCents: 0, sendable: false });
});

// ── the split panel's pre-ring read: the typed sum and the shape, no total ────────────────────
test('splitDraftRead sums the typed legs and words the shape, with no total in it', () => {
  const read = splitDraftRead(
    [
      { method: 'CASH', amountCents: 400 },
      { method: 'CARD', amountCents: 52 },
    ],
    false,
    true,
  );
  assert.deepEqual(read, { typedCents: 452, problem: '' });
  // A leg that has not parsed contributes nothing to the sum and refuses the shape.
  const half = splitDraftRead(
    [
      { method: 'CASH', amountCents: 452 },
      { method: 'CARD', amountCents: NaN },
    ],
    false,
    true,
  );
  assert.equal(half.typedCents, 452);
  assert.match(half.problem, /amount greater than zero/);
  // The shape refusals are the server's own rules, worded.
  assert.match(splitDraftRead([{ method: 'CASH', amountCents: 452 }], false, true).problem, /at least two/);
  assert.match(
    splitDraftRead(
      [
        { method: 'CASH', amountCents: 100 },
        { method: 'ACCOUNT', amountCents: 352 },
      ],
      false,
      true,
    ).problem,
    /Pick a customer account/,
  );
  assert.match(
    splitDraftRead(
      [
        { method: 'CASH', amountCents: 100 },
        { method: 'CARD', amountCents: 352 },
      ],
      false,
      false,
    ).problem,
    /Add a line/,
  );
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
  // The rate is respected: a different rate answers a different line, so a
  // seam that ignored its parameter for a hardcoded rate fails here.
  assert.equal(ticketTaxCents(10000, 1500), 1500);
  assert.equal(ticketTaxCents(10000, 1200), 1200);
  // Rounded to the whole cent where rounding and flooring disagree:
  // 1958 at 1300 bps is 254.54, which rounds to 255 and floors to 254.
  assert.equal(ticketTaxCents(1958, 1300), 255);
  // The total carries the estimated tax line on the assembled subtotal.
  assert.equal(ticketTotalCents(10000, 1300), 11300);
  assert.equal(ticketTotalCents(6400, 832), 7232);
});

test('sumLineSavingCents multiplies per line and sums, skipping what the engine left out', () => {
  assert.equal(sumLineSavingCents([{ saving_cents: 72, qty: 3 }, { saving_cents: 140, qty: 2 }]), 496);
  assert.equal(sumLineSavingCents([{ saving_cents: NaN, qty: 3 }, { saving_cents: 140, qty: 2 }]), 280);
  assert.equal(sumLineSavingCents([]), 0);
});

test('a fractional quantity leaves integer cents: each line rounds as it sums', () => {
  // One third of a CA$0.72 saving is 24 cents once rounded, never 24.000...,
  // and the formatter never sees a fraction.
  assert.equal(perLineSavingCents(72, 1 / 3), 24);
  // Two half-quantity lines round per line (13 and 13), not once at the end
  // (which would answer 26 from 12.5 and 12.5): the reference's discipline.
  assert.equal(
    sumLineSavingCents([{ saving_cents: 25, qty: 0.5 }, { saving_cents: 25, qty: 0.5 }]),
    26,
  );
  // The same rule on the read-back's per-line word.
  assert.equal(perLineSavingCents(25, 0.5), 13);
  // A figure the engine left out is still nothing.
  assert.equal(perLineSavingCents(NaN, 2), 0);
});

test('balanceAfterDepositCents answers the balance, never below zero', () => {
  assert.equal(balanceAfterDepositCents(TOTAL, 5000), 6300);
  assert.equal(balanceAfterDepositCents(100, 500), 0);
});

// ── the refund estimate and the credit headroom, over the lookup's own figures ───────────────
// The shapes below are the contract's PosReturnableLine: unit_price_cents
// prices ONE unit and line_total_cents is the whole line's, so a line of two
// Holland pavers at CA$60.00 each carries unit 6000 and line total 12000.
// The estimate multiplies the UNIT price by the picked quantity, per line,
// rounded per line, exactly as the binding refund does.
test('refundEstimateCents prices the picked quantity at the unit price, per line', () => {
  const holland = { order_line_id: 'a', unit_price_cents: 6000, tax_cents: 0, qty_sold: 2 };
  // One of two pavers picked: the binding refund is CA$60.00, and the
  // estimate says CA$60.00 (one unit), never half of one unit.
  const one = refundEstimateCents([holland], { a: 1 });
  assert.deepEqual(one, { subtotal: 6000, tax: 0, total: 6000 });
  // Both picked: the whole line's own total.
  const both = refundEstimateCents([holland], { a: 2 });
  assert.deepEqual(both, { subtotal: 12000, tax: 0, total: 12000 });

  const wallblock = { order_line_id: 'b', unit_price_cents: 6000, tax_cents: 0, qty_sold: 6 };
  // Six wall blocks picked whole: the line's own total comes back.
  const full = refundEstimateCents([wallblock], { b: 6 });
  assert.deepEqual(full, { subtotal: 36000, tax: 0, total: 36000 });
});

test('refundEstimateCents spreads the line tax across the sold quantity, rounded per line', () => {
  const line = { order_line_id: 't', unit_price_cents: 6000, tax_cents: 780, qty_sold: 2 };
  // One of two picked: half the line's tax, rounded to the whole cent.
  const half = refundEstimateCents([line], { t: 1 });
  assert.deepEqual(half, { subtotal: 6000, tax: 390, total: 6390 });
  // A line with no sold quantity carries no tax leg (there is nothing to
  // spread over): its price comes back, its tax does not.
  const odd = { order_line_id: 'z', unit_price_cents: 5000, tax_cents: 400, qty_sold: 0 };
  const untaxed = refundEstimateCents([odd], { z: 1 });
  assert.deepEqual(untaxed, { subtotal: 5000, tax: 0, total: 5000 });
});

test('refundEstimateCents rounds per line, so fractional picks stay integer cents', () => {
  // Half of each of two CA$33.33 units rounds per line (1667 and 1667), not
  // once at the end (which would answer 3333): the reference's discipline.
  const a = { order_line_id: 'a', unit_price_cents: 3333, tax_cents: 0, qty_sold: 2 };
  const b = { order_line_id: 'b', unit_price_cents: 3333, tax_cents: 0, qty_sold: 2 };
  const halves = refundEstimateCents([a, b], { a: 0.5, b: 0.5 });
  assert.deepEqual(halves, { subtotal: 3334, tax: 0, total: 3334 });
});

test('refundEstimateCents answers nothing for a draft with no picked line', () => {
  const lines = [
    { order_line_id: 'a', unit_price_cents: 6000, tax_cents: 0, qty_sold: 2 },
    { order_line_id: 'b', unit_price_cents: 6000, tax_cents: 780, qty_sold: 6 },
  ];
  const none = refundEstimateCents(lines, {});
  assert.deepEqual(none, { subtotal: 0, tax: 0, total: 0 });
});

test('availableCreditCents clamps the headroom at zero, never a negative offer', () => {
  assert.equal(availableCreditCents({ credit_limit_cents: 500000, open_balance_cents: 125000 }), 375000);
  assert.equal(availableCreditCents({ credit_limit_cents: 100000, open_balance_cents: 125000 }), 0);
  assert.equal(availableCreditCents({ credit_limit_cents: NaN, open_balance_cents: 0 }), 0);
});
