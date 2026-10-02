// Ported from hardscapeos_dibbits master 87d7941b app/src/order-logic.test.mjs; adapted for HH.

// order-logic.test.mjs: the orders module's lifecycle vocabulary, pinned value by value.
//
// THE RULE these words exist to satisfy: client-facing copy carries zero internal engineering
// vocabulary. PARTIALLY_FULFILLED and PENDING_YARD are rows in a state machine; nobody at a counter
// says them, and a chip that prints one is showing a database column where a word belongs.
//
// WHAT IS PINNED HERE
// -------------------
// · every value the three CHECK constraints allow has a word, and the words are the ones the
// product ALREADY uses for those states: the meaning may not change, which is the acceptance
// clause of the item that asked for this;
// · the DEFAULT ARM degrades HONESTLY: an unmapped value comes back as itself, lowercased with its
// underscores opened out, never "Unknown" and never ''. That is the arm that keeps a state added
// to a migration backend-first VISIBLE instead of silently mistranslated;
// · the chip budget: sentence case, at most two words, so a status cannot reflow a dense column.
//
// The maps are DOM-free, so they import here directly: no browser, no component.
//
// node --import./src/_ts-js-loader.mjs --test src/order-logic.test.mjs (run from app/)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ORDER_STATUS_LABEL,
  ORDER_STATUS_PHRASE,
  CHANGE_STATUS_LABEL,
  GROUP_STATUS_LABEL,
  orderStatusWord,
  orderStatusPhrase,
  changeStatusWord,
  groupStatusWord,
} from './order-logic.js';

// The three columns' values are typed out here FROM THE MIGRATIONS rather than derived from the maps
// under test: a value quietly dropped from a map must fail this file, not shrink the assertion with
// it. Line references are to the CHECK constraint each list came from.
const ORDER_STATUSES_0008 = [
  'DRAFT',
  'CONFIRMED',
  'PICKING',
  'READY',
  'PARTIALLY_FULFILLED',
  'FULFILLED',
  'INVOICED',
  'CLOSED',
  'CANCELLED',
]; // migrations/0008_quotes_orders.sql:51-52
const CHANGE_STATUSES_0125 = [
  'DRAFT',
  'PENDING_YARD',
  'PENDING_FINANCE',
  'APPROVED',
  'APPLIED',
  'REJECTED',
  'CANCELLED',
]; // migrations/0125_order_change_orders.sql:95-96
const GROUP_STATUSES_0126 = [
  'PLANNED',
  'PICKING',
  'READY',
  'DISPATCHED',
  'COMPLETE',
  'CANCELLED',
]; // migrations/0126_order_shipment_groups.sql:91-92

// ── orders.status ──────────────────────────────────────────────────────────────────────────────

test('orderStatusWord gives every stored order state its chip word', () => {
 // These are the product's OWN words, not new ones: order-history.ts's fmtStatus, the server's
 // activity feed (order/activity.go humanStatus) and the assistant (assist statusWord) have all
 // said "Partially fulfilled" since they shipped. The map reuses that rather than minting a rival.
  assert.equal(orderStatusWord('DRAFT'), 'Draft');
  assert.equal(orderStatusWord('CONFIRMED'), 'Confirmed');
  assert.equal(orderStatusWord('PICKING'), 'Picking');
  assert.equal(orderStatusWord('READY'), 'Ready');
  assert.equal(orderStatusWord('PARTIALLY_FULFILLED'), 'Partially fulfilled');
  assert.equal(orderStatusWord('FULFILLED'), 'Fulfilled');
  assert.equal(orderStatusWord('INVOICED'), 'Invoiced');
  assert.equal(orderStatusWord('CLOSED'), 'Closed');
  assert.equal(orderStatusWord('CANCELLED'), 'Cancelled');
});

test('orderStatusPhrase says the same nine states mid-sentence', () => {
 // The slot is the lifecycle panel's stale-view 409: "order is …: confirming is no longer
 // available". DRAFT needs its article to be a sentence at all; PICKING is passive because an order
 // does not "is picking". The server phrases its half the same way (willcall.go orderStateWord).
  assert.equal(`order is ${orderStatusPhrase('DRAFT')}`, 'order is a draft');
  assert.equal(`order is ${orderStatusPhrase('CONFIRMED')}`, 'order is confirmed');
  assert.equal(`order is ${orderStatusPhrase('PICKING')}`, 'order is being picked');
  assert.equal(`order is ${orderStatusPhrase('READY')}`, 'order is ready');
  assert.equal(`order is ${orderStatusPhrase('PARTIALLY_FULFILLED')}`, 'order is partially fulfilled');
  assert.equal(`order is ${orderStatusPhrase('FULFILLED')}`, 'order is fulfilled');
  assert.equal(`order is ${orderStatusPhrase('INVOICED')}`, 'order is invoiced');
  assert.equal(`order is ${orderStatusPhrase('CLOSED')}`, 'order is closed');
  assert.equal(`order is ${orderStatusPhrase('CANCELLED')}`, 'order is cancelled');
});

test('both order maps cover every value the 0008 CHECK allows', () => {
  for (const s of ORDER_STATUSES_0008) {
    assert.ok(ORDER_STATUS_LABEL[s], `no chip word for ${s}`);
    assert.ok(ORDER_STATUS_PHRASE[s], `no sentence word for ${s}`);
 // Nothing may fall through to the honest-degrade arm while the value is in the CHECK.
    assert.notEqual(orderStatusWord(s), s);
  }
  assert.equal(Object.keys(ORDER_STATUS_LABEL).length, ORDER_STATUSES_0008.length);
  assert.equal(Object.keys(ORDER_STATUS_PHRASE).length, ORDER_STATUSES_0008.length);
});

// ── change_orders.status ───────────────────────────────────────────────────────────────────────

test('changeStatusWord names who a change order is with, not which queue it is in', () => {
 // PENDING_YARD is the name of a queue. The chip says who has it, which is the fact the person
 // looking at it needs: and the same fact changeStageSummary spells out underneath ("Waiting on
 // the yard."). CANCELLED is 'Withdrawn' because 0125 defines it as the requester withdrawing, the
 // button says "Withdraw…" and the stage summary already says "Withdrawn.".
  assert.equal(changeStatusWord('DRAFT'), 'Draft');
  assert.equal(changeStatusWord('PENDING_YARD'), 'With yard');
  assert.equal(changeStatusWord('PENDING_FINANCE'), 'With finance');
  assert.equal(changeStatusWord('APPROVED'), 'Approved');
  assert.equal(changeStatusWord('APPLIED'), 'Applied');
  assert.equal(changeStatusWord('REJECTED'), 'Rejected');
  assert.equal(changeStatusWord('CANCELLED'), 'Withdrawn');
});

test('CHANGE_STATUS_LABEL covers every value the 0125 CHECK allows', () => {
  for (const s of CHANGE_STATUSES_0125) {
    assert.ok(CHANGE_STATUS_LABEL[s], `no word for ${s}`);
    assert.notEqual(changeStatusWord(s), s);
  }
  assert.equal(Object.keys(CHANGE_STATUS_LABEL).length, CHANGE_STATUSES_0125.length);
});

// ── shipment_groups.status ─────────────────────────────────────────────────────────────────────

test('groupStatusWord speaks the order ladder, because 0126 says it is the same ladder', () => {
 // 0126: the group ladder deliberately mirrors the order one so "a yard lead who knows what READY
 // means on an order knows what it means on a group". One state, one word, on both.
  assert.equal(groupStatusWord('PICKING'), orderStatusWord('PICKING'));
  assert.equal(groupStatusWord('READY'), orderStatusWord('READY'));
  assert.equal(groupStatusWord('CANCELLED'), orderStatusWord('CANCELLED'));

  assert.equal(groupStatusWord('PLANNED'), 'Planned');
  assert.equal(groupStatusWord('DISPATCHED'), 'Dispatched');
 // COMPLETE is reachable only from DISPATCHED, and this surface already calls that "delivered":
 // "One invoice when every group is delivered", and the button "Mark delivered".
  assert.equal(groupStatusWord('COMPLETE'), 'Delivered');
});

test('GROUP_STATUS_LABEL covers every value the 0126 CHECK allows', () => {
  for (const s of GROUP_STATUSES_0126) {
    assert.ok(GROUP_STATUS_LABEL[s], `no word for ${s}`);
    assert.notEqual(groupStatusWord(s), s);
  }
  assert.equal(Object.keys(GROUP_STATUS_LABEL).length, GROUP_STATUSES_0126.length);
});

// ── the arm that keeps a schema change visible ─────────────────────────────────────────────────

test('an unmapped state degrades honestly rather than lying', () => {
 // A tenth order status, an eighth change status, a seventh group status: whichever arrives, it
 // shows up as STRANGE rather than as a confident wrong word. Never 'Unknown', never ''.
  for (const f of [orderStatusWord, orderStatusPhrase, changeStatusWord, groupStatusWord]) {
    assert.equal(f('SOME_NEW_STATE'), 'some new state');
    assert.equal(f('AWAITING_PICK'), 'awaiting pick');
    assert.equal(f('BACKORDERED'), 'backordered');
    assert.notEqual(f('SOME_NEW_STATE'), 'Unknown');
    assert.notEqual(f('SOME_NEW_STATE'), '');
    assert.equal(f(''), '');
  }
});

test('no status word shouts, and every chip word fits the tag budget', () => {
 // Design budget: chips are one word, two for a status compound, sentence case. A chip that reflows
 // a dense table column is a chip that broke the layout, and SHOUTING is the thing being removed.
  const chips = [
    [ORDER_STATUSES_0008, orderStatusWord],
    [CHANGE_STATUSES_0125, changeStatusWord],
    [GROUP_STATUSES_0126, groupStatusWord],
  ];
  for (const [values, word] of chips) {
    for (const s of values) {
      const w = word(s);
      assert.notEqual(w, w.toUpperCase(), `${s} still shouts`);
      assert.ok(!w.includes('_'), `${s} still carries an underscore`);
      assert.ok(w.split(' ').length <= 2, `${s} is too long for a chip: ${w}`);
      assert.equal(w[0], w[0].toUpperCase(), `${s} does not start its sentence`);
      assert.equal(w.slice(1), w.slice(1).toLowerCase(), `${s} is Title Case, not sentence case`);
    }
  }
});
