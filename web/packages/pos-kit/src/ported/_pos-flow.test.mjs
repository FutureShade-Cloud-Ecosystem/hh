// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-flow.test.mjs; adapted for HH.

// DIB-488: the sale-session flow derivation (_pos-flow.ts). Runs on the Node built-in test runner:
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (run from app/)
//
// The stepper is TRACKED, NOT GATED, and derived: these tests pin the whole state table so the
// stepper can never drift into wizard behavior (a manually-advanced step would have no data to
// derive from and would fail here).
//
// RESHAPED (POS restructure, client-directed after a day at the counter): the order is now
// 1 Customer → 2 Items → 3 Payment → 4 Summary. Customer moved to the FRONT because every price
// below it depends on which account is on the sale, and Summary is a new fourth step holding the
// receipt moment. Three assertions below changed with it: the first-open step of a fresh sale,
// the not-gated case's reported focus, and the numbering: and they are listed in the NOTES
// mutation ledger as a deliberate contract change, not a drift. What did NOT change is the ruling
// property, re-pinned harder here: nothing in this module can refuse anything.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deriveFlow } from './_pos-flow.ts';

const facts = (over = {}) => ({
  lineCount: 0,
  customerSelected: false,
  walkInConfirmed: false,
  payMethod: '',
  rungDone: false,
  ...over,
});

const byId = (steps) => Object.fromEntries(steps.map((s) => [s.id, s]));

test('DIB-488: a fresh sale: nothing complete, Customer is current', () => {
  const s = byId(deriveFlow(facts()));
  assert.equal(s.customer.complete, false);
  assert.equal(s.items.complete, false);
  assert.equal(s.payment.complete, false);
  assert.equal(s.summary.complete, false);
  assert.equal(s.customer.current, true); // reshaped: Customer is step 1 and opens first
  assert.equal(s.items.current, false);
  assert.equal(s.payment.current, false);
  assert.equal(s.summary.current, false);
});

test('DIB-488: a line on the ticket completes Items: an unanswered Customer still reports first', () => {
  const s = byId(deriveFlow(facts({ lineCount: 2 })));
  assert.equal(s.items.complete, true);
 // Scanning before anyone touches the customer form is the WALK-IN PATH, and it is allowed: the
 // ticket takes the line, Items reads complete, and the stepper simply keeps reporting that the
 // customer question is still open. It does not block, and it does not stop reporting either.
  assert.equal(s.customer.current, true);
});

test('DIB-488: an account customer resolves step 2', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1, customerSelected: true })));
  assert.equal(s.customer.complete, true);
  assert.equal(s.payment.current, true);
});

test('DIB-488: explicitly confirmed walk-in ALSO resolves step 2: walk-in is an answer, not a gap', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1, walkInConfirmed: true })));
  assert.equal(s.customer.complete, true);
  assert.equal(s.payment.current, true);
});

test('DIB-488: the DEFAULT walk-in posture does not complete step 2: a default is not a decision', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1 })));
  assert.equal(s.customer.complete, false);
  assert.equal(s.customer.current, true);
});

test('DIB-488: a chosen tender completes Payment; all complete rests current on Payment', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1, customerSelected: true, payMethod: 'CASH' })));
  assert.equal(s.payment.complete, true);
  assert.equal(s.payment.current, true); // the ready-to-complete posture
  assert.equal(s.items.current, false);
  assert.equal(s.customer.current, false);
});

test('DIB-488: NOT GATED: steps complete in any order (payment before customer before items)', () => {
  const s = byId(deriveFlow(facts({ payMethod: 'CARD' })));
  assert.equal(s.payment.complete, true);
  assert.equal(s.items.complete, false);
  assert.equal(s.customer.complete, false);
  assert.equal(s.customer.current, true); // focus reports the first open step; nothing is locked
});

test('DIB-488: DERIVED: removing the last line un-completes Items again', () => {
  const before = byId(deriveFlow(facts({ lineCount: 1, customerSelected: true })));
  assert.equal(before.items.complete, true);
  const after = byId(deriveFlow(facts({ lineCount: 0, customerSelected: true })));
  assert.equal(after.items.complete, false);
  assert.equal(after.items.current, true);
});

test('DIB-488: a committed ring satisfies every step', () => {
  const s = byId(deriveFlow(facts({ rungDone: true })));
  assert.equal(s.items.complete && s.customer.complete && s.payment.complete, true);
  assert.equal(s.summary.complete, true);
});

test('DIB-488: exactly one step is current in every state', () => {
  const bools = [false, true];
  for (const lines of [0, 1])
    for (const cust of bools)
      for (const walkin of bools)
        for (const pay of ['', 'CASH'])
          for (const rung of bools) {
            const steps = deriveFlow(
              facts({ lineCount: lines, customerSelected: cust, walkInConfirmed: walkin, payMethod: pay, rungDone: rung }),
            );
            const current = steps.filter((s) => s.current);
            assert.equal(current.length, 1, `state ${JSON.stringify([lines, cust, walkin, pay, rung])}`);
          }
});

test('POS restructure: the numbering and order are 1 Customer → 2 Items → 3 Payment → 4 Summary', () => {
  const steps = deriveFlow(facts());
  assert.deepEqual(
    steps.map((s) => [s.n, s.id]),
    [
      [1, 'customer'],
      [2, 'items'],
      [3, 'payment'],
      [4, 'summary'],
    ],
  );
  assert.deepEqual(steps.map((s) => s.label), ['Customer', 'Items', 'Payment', 'Summary']);
});

// ── the new fourth step ────────────────────────────────────────────────────────────────────────
//
// Summary holds the receipt moment. The one property worth pinning hardest is the NEGATIVE one: an
// un-rung sale must never point the operator at a receipt that does not exist, no matter how much
// of the rest of the sale is finished.

test('POS restructure: Summary is complete ONLY on a committed ring', () => {
  const done = byId(deriveFlow(facts({ lineCount: 3, customerSelected: true, payMethod: 'CASH' })));
  assert.equal(done.summary.complete, false); // everything else finished, still no receipt
  const rung = byId(deriveFlow(facts({ lineCount: 3, customerSelected: true, payMethod: 'CASH', rungDone: true })));
  assert.equal(rung.summary.complete, true);
});

test('POS restructure: a ready-to-ring sale rests on Payment, never on Summary', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1, customerSelected: true, payMethod: 'CASH' })));
  assert.equal(s.payment.current, true);
  assert.equal(s.summary.current, false);
});

test('POS restructure: the committed ring: and only it: moves current to Summary', () => {
  const s = byId(deriveFlow(facts({ lineCount: 1, customerSelected: true, payMethod: 'CASH', rungDone: true })));
  assert.equal(s.summary.current, true);
  assert.equal(s.payment.current, false);
});

test('POS restructure: Summary never becomes current on an un-rung sale, in ANY other state', () => {
  const bools = [false, true];
  for (const lines of [0, 1, 5])
    for (const cust of bools)
      for (const walkin of bools)
        for (const pay of ['', 'CASH', 'CARD'])
          assert.equal(
            byId(deriveFlow(facts({ lineCount: lines, customerSelected: cust, walkInConfirmed: walkin, payMethod: pay })))
              .summary.current,
            false,
            `un-rung state ${JSON.stringify([lines, cust, walkin, pay])} pointed at a receipt`,
          );
});

test('POS restructure: THE WALK-IN CASH SALE: scanned and rung without touching the customer form', () => {
 // The exact path the client refuses to have gated: no account, no walk-in confirmation, just a
 // scan and a tender. Nothing in the derivation may report a blocked or unreachable step.
  const scanned = byId(deriveFlow(facts({ lineCount: 1 })));
  assert.equal(scanned.items.complete, true);
  const tendered = byId(deriveFlow(facts({ lineCount: 1, payMethod: 'CASH' })));
  assert.equal(tendered.payment.complete, true); // reachable with Customer still unanswered
  const rung = byId(deriveFlow(facts({ lineCount: 1, payMethod: 'CASH', rungDone: true })));
  assert.equal(rung.summary.current, true);
 // And the ring back-fills the unanswered step honestly rather than leaving a sold ticket
 // showing an outstanding question.
  assert.equal(rung.customer.complete, true);
});

test('POS restructure: DERIVED: un-doing the ring un-completes Summary', () => {
  assert.equal(byId(deriveFlow(facts({ rungDone: true }))).summary.complete, true);
  assert.equal(byId(deriveFlow(facts({ rungDone: false }))).summary.complete, false);
});
