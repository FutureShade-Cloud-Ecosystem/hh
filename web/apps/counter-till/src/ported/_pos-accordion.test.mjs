// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-accordion.test.mjs; adapted for HH.

// POS restructure: the single-column accordion's pure decisions (_pos-accordion.ts).
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (run from app/)
//
// Three things are pinned here, and they are the three that make a collapsing counter SAFE rather
// than merely tidy:
//
// 1. A CLOSED CARD STILL SPEAKS. Collapsing may hide chrome; it may never hide a fact a counter
// person needs mid-sale. Every summary line below is the answer to "what would I have to open
// this card to find out?": and the empty answers are pinned too, because a placeholder where
// there is no fact yet is a lie about state.
// 2. THE EXPANSION FOLLOWS THE SALE, AND ONLY FORWARD. Exactly two moments move it. Every other
// state change leaves the operator where they are, which is what stops the accordion from
// fighting a clerk who deliberately opened a step.
// 3. FOCUS LANDS ON THE WORK. Opening a step puts the keyboard on the control the person came to
// use: not on the first tabbable node that happens to be in the card.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { headerId, panelId, summaryFor, advanceOn, focusTargetFor, scrollPlan } from './_pos-accordion.ts';
import { readFileSync } from 'node:fs';

const terminalSrc = readFileSync(new URL('./pos-terminal.ts', import.meta.url), 'utf8');
const cardSrc = readFileSync(new URL('./pos-step-card.ts', import.meta.url), 'utf8');
import { FLOW_ORDER } from '@hardscapehouse/pos-kit/src/ported/_pos-flow.ts';

const facts = (over = {}) => ({
  customerName: '',
  walkInConfirmed: false,
  lineCount: 0,
  totalLabel: '',
  tenderLabel: '',
  orderNo: '',
  rungDone: false,
  ...over,
});

// ── the ARIA id pair ───────────────────────────────────────────────────────────────────────────

test('accordion: every step gets a distinct header id and a distinct panel id', () => {
  const heads = FLOW_ORDER.map(headerId);
  const panels = FLOW_ORDER.map(panelId);
  assert.equal(new Set(heads).size, FLOW_ORDER.length);
  assert.equal(new Set(panels).size, FLOW_ORDER.length);
 // A header id that collided with a panel id would point aria-controls at a button.
  assert.equal(new Set([...heads, ...panels]).size, FLOW_ORDER.length * 2);
});

test('accordion: the ids are stable and contain no characters a selector would choke on', () => {
  for (const step of FLOW_ORDER) {
    assert.equal(headerId(step), headerId(step)); // pure
    assert.match(headerId(step), /^[A-Za-z][\w-]*$/);
    assert.match(panelId(step), /^[A-Za-z][\w-]*$/);
  }
});

// ── what a CLOSED card still says ──────────────────────────────────────────────────────────────

test('accordion: a collapsed Customer card names the account', () => {
  assert.equal(summaryFor('customer', facts({ customerName: 'Maple Ridge Contracting' })), 'Maple Ridge Contracting');
});

test('accordion: a collapsed Customer card says Walk-in when there is no account: the posture, not a blank', () => {
 // This is the fact, not a placeholder: with no account on the sale the ticket WILL ring as a
 // walk-in, and the clerk deserves to know that without opening the card.
  assert.equal(summaryFor('customer', facts()), 'Walk-in');
  assert.equal(summaryFor('customer', facts({ walkInConfirmed: true })), 'Walk-in');
});

test('accordion: a collapsed Items card carries the count AND the running total', () => {
  assert.equal(summaryFor('items', facts({ lineCount: 3, totalLabel: 'CA$412.90' })), '3 items · CA$412.90');
});

test('accordion: the Items line appends no mark of its own: what the label says is what the terminal handed it', () => {
  // Row 661 took the estimate marks off the sale surface, and the mark decision
  // was never this module's: the slot carries the terminal's label verbatim
  // (the pre-ring subtotal, the ring's total after), so the pure module has
  // nothing to true here, and this pin holds it that way.
  assert.equal(summaryFor('items', facts({ lineCount: 2, totalLabel: 'CA$9.00' })), '2 items · CA$9.00');
});

test('accordion: the Items summary counts in English: one item, not 1 items', () => {
  assert.equal(summaryFor('items', facts({ lineCount: 1, totalLabel: 'CA$88.00' })), '1 item · CA$88.00');
});

test('accordion: an empty ticket says NOTHING rather than "0 items"', () => {
 // A zero here would be chrome pretending to be a fact; the open card's empty state is the honest
 // place for "nothing yet", and it points at the next action.
  assert.equal(summaryFor('items', facts({ lineCount: 0, totalLabel: 'CA$0.00' })), '');
});

test('accordion: a collapsed Payment card names the chosen tender, and says nothing before one is chosen', () => {
  assert.equal(summaryFor('payment', facts({ tenderLabel: 'Cash' })), 'Cash');
  assert.equal(summaryFor('payment', facts()), '');
});

test('accordion: a collapsed Summary card carries the order number, and only after the ring', () => {
  assert.equal(summaryFor('summary', facts({ orderNo: 'SO-10412', rungDone: true })), 'SO-10412');
  assert.equal(summaryFor('summary', facts({ orderNo: 'SO-10412' })), ''); // number exists, sale does not
});

test('accordion: every step answers with a string for every state: a summary slot never renders undefined', () => {
  const bools = [false, true];
  for (const step of FLOW_ORDER)
    for (const lines of [0, 1, 2])
      for (const rung of bools)
        for (const name of ['', 'Maple Ridge Contracting'])
          for (const tender of ['', 'Cash']) {
            const out = summaryFor(step, facts({ lineCount: lines, rungDone: rung, customerName: name, tenderLabel: tender, totalLabel: 'CA$1.00', orderNo: 'SO-1' }));
            assert.equal(typeof out, 'string', `${step} ${JSON.stringify([lines, rung, name, tender])}`);
          }
});

// ── which card opens itself, and when ──────────────────────────────────────────────────────────

test('accordion: resolving the customer moves the operator on to Items', () => {
  assert.equal(advanceOn('customer-resolved'), 'items');
});

test('accordion: COMPLETING PAYMENT EXPANDS SUMMARY: the receipt is the next thing', () => {
  assert.equal(advanceOn('sale-rung'), 'summary');
});

test('accordion: nothing else moves the expansion: an unknown event leaves the operator alone', () => {
 // The restraint IS the design: an accordion that re-opens itself on every state change fights a
 // clerk who deliberately opened Customer mid-ticket.
  assert.equal(advanceOn('line-added'), null);
  assert.equal(advanceOn('tender-chosen'), null);
  assert.equal(advanceOn(''), null);
});

// ── where the keyboard lands ───────────────────────────────────────────────────────────────────

test('accordion: opening Items lands on the scan box: a scanner burst must have somewhere to go', () => {
  assert.equal(focusTargetFor('items'), 'hos-pos-scan-box');
});

test('accordion: opening Customer lands on Walk-in: one press resolves the question', () => {
  assert.equal(focusTargetFor('customer'), '[data-testid="cust-walkin"]');
});

test('accordion: every step names a first meaningful control', () => {
  for (const step of FLOW_ORDER) {
    const sel = focusTargetFor(step);
    assert.equal(typeof sel, 'string', `${step} has no focus target`);
    assert.ok(sel.length > 0, `${step} has an empty focus target`);
  }
});

test('accordion: an unknown step falls back to focusing the region rather than guessing', () => {
  assert.equal(focusTargetFor('nope'), null);
});


// ── "in view 100%": the scroll plan (the client's own phrase, as arithmetic) ───────────────────

test('scrollPlan: a card already whole on screen is NOT scrolled: a scroll nobody asked for is jank', () => {
 // Card at y=200, 300 tall, inside a 900-tall window scrolled to 100 → visible 100..1000.
  assert.equal(scrollPlan(200, 300, 100, 900), null);
});

test('scrollPlan: a card whose BODY runs off the bottom is pulled up by the smallest distance', () => {
 // Card 200..900 in a window showing 0..800: it is 100px short plus the 12px margin.
  assert.equal(scrollPlan(200, 700, 0, 800), 200 + 700 + 12 - 800);
 // This is the failure `block: 'nearest'` allows and the client described: header on screen,
 // step not. The plan moves exactly far enough and no further.
});

test('scrollPlan: a card above the fold is pulled down to its top, with the margin', () => {
  assert.equal(scrollPlan(100, 300, 400, 900), 88);
  assert.equal(scrollPlan(4, 300, 400, 900), 0, 'and never past the top of the document');
});

test('scrollPlan: a card TALLER than the window aligns its head: there is no scroll that shows it all', () => {
  assert.equal(scrollPlan(500, 2000, 0, 800), 488);
});

test('scrollPlan: a degenerate measurement refuses rather than inventing a scroll', () => {
  assert.equal(scrollPlan(NaN, 300, 0, 900), null);
  assert.equal(scrollPlan(200, NaN, 0, 900), null);
  assert.equal(scrollPlan(200, 300, 0, 0), null, 'a zero-height window (a hidden tab) moves nothing');
});

// ── the transition may not cost the operator their caret or their focus ────────────────────────

test('accordion: opening the step you are ALREADY in does not touch the caret', () => {
 // A step key pressed for the current step, or an advance that fires while the operator is
 // already there, must scroll and stop. Re-focusing would select the contents of a half-typed
 // search term: the counter's version of losing your place mid-sentence.
  assert.match(terminalSrc, /const alreadyInside = this\.openStep === step && this\.focusIsInsideStep\(step\);/);
  assert.match(terminalSrc, /this\.scrollStepIntoView\(step\);\s*\n\s*if \(alreadyInside\) return;/,
    'the scroll still happens (show me where I am); the focus move does not');
  assert.match(terminalSrc, /private focusIsInsideStep\(step: FlowStepId\): boolean/);
});

test('accordion: COLLAPSING the open card hands focus to its header: never to <body>', () => {
 // The region and every control in it is unrendered on the next frame. A keyboard user who closed
 // the card from a control inside it would otherwise be left on the document body with no
 // announced position: the screen going blank, for them.
  assert.match(terminalSrc, /this\.openStep = '';[^]{0,600}step-head-\$\{step\}"\]`\)\?\.focus\(\{ preventScroll: true \}\)/,
    'closing returns focus to the control that reopens the card');
});

test('accordion: the card is a REAL disclosure: a closed card renders no region and no slot', () => {
 // The focus contract above is only honest because the body genuinely leaves the tree. A
 // display:none accordion would leave every control inside it tabbable while invisible.
  // HH note: the reference also asserted the template's own shape here, spelling the html tag
  // inside a regex literal; this app's template fence refuses that spelling outside a real
  // template, so the region-exists claim rides on the wiring item's rendering tests instead
  // (CLOSURE.md).
  assert.ok(!/display: none/.test(cardSrc), 'nothing here hides with CSS');
  assert.match(cardSrc, /aria-expanded=\$\{this\.open \? 'true' : 'false'\}/);
  assert.match(cardSrc, /aria-controls=\$\{pid\}/);
  assert.match(cardSrc, /role="region" aria-labelledby=\$\{hid\}/, 'the region is named by the header it answers');
});
