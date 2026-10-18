// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/_order-depth.test.mjs
// (the change-order half); adapted for HH, with the lane's own additions (the wall words, the
// refusal mapping and the status pick) asserted beside the ported ones.

// Regression for the pure change-order helpers (_order-change.ts), SCOPE:F10.
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test src/ported/_order-change.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  canDoChangeAction,
  changeActionLabel,
  changeActionPath,
  offeredChangeActions,
  changeStageSummary,
  changeStatusChip,
  describeDelta,
  formatQty,
  refusalWords,
  statusIsPostPick,
  thrownMessage,
  wallWords,
  ACTION_APPLY,
  ACTION_CANCEL,
  ACTION_FINANCE_APPROVE,
  ACTION_YARD_ACK,
} from './_order-change.ts';

const change = (over = {}) => ({
  id: 'c1',
  order_id: 'o1',
  branch_id: 'b1',
  change_no: 'CHG-0001',
  status: 'PENDING_YARD',
  reason: '',
  delta_cents: 1200,
  finance_threshold_cents: 50000,
  finance_required: false,
  order_status_at_request: 'PICKING',
  pick_started_at_request: true,
  requested_at: '2026-08-06T00:00:00Z',
  lines: [],
  ...over,
});

// -- the client renders from the SERVER's ladder, never its own ----------------------------------

test('canDoChangeAction offers only what the server listed', () => {
  const co = change({ next_actions: [ACTION_YARD_ACK, 'reject'] });
  assert.equal(canDoChangeAction(co, ACTION_YARD_ACK), true);
  assert.equal(canDoChangeAction(co, ACTION_APPLY), false);
});

test('offeredChangeActions drops an action this surface has no endpoint for (the one tap is Manager Home\'s)', () => {
  const co = change({ next_actions: ['sign_apply', ACTION_APPLY, 'reject', 'cancel'] });
  assert.deepEqual(offeredChangeActions(co), [ACTION_APPLY, 'reject', 'cancel']);
  assert.deepEqual(offeredChangeActions(change()), []);
  assert.deepEqual(offeredChangeActions(null), []);
});

test('an absent next_actions offers NOTHING (fail closed, never unlock everything)', () => {
  // An older server that predates SCOPE:F10 sends no next_actions. Defaulting to "offer everything"
  // would paint an Apply button whose write the server refuses: worse, it would suggest to a
  // manager that a change with no yard acknowledgement is ready to go.
  assert.equal(canDoChangeAction(change(), ACTION_APPLY), false);
  assert.equal(canDoChangeAction(null, ACTION_APPLY), false);
});

test('apply is never offered on a change the server has not approved', () => {
  // The server computes this, but the assertion pins the contract the client relies on: whatever
  // arrives in next_actions for a pre-approval state, "apply" is not in it.
  for (const status of ['PENDING_YARD', 'PENDING_FINANCE']) {
    const co = change({ status, next_actions: [ACTION_YARD_ACK, 'reject', 'cancel'] });
    assert.equal(canDoChangeAction(co, ACTION_APPLY), false, status);
  }
});

test('an action this client does not know maps to no path rather than a wrong one', () => {
  // A backend that adds an action later must not have its button POST to a guessed endpoint.
  assert.equal(changeActionPath('escalate'), '');
  assert.equal(changeActionPath(ACTION_APPLY), 'apply');
  assert.equal(changeActionPath(ACTION_YARD_ACK), 'yard-ack');
});

test('an unknown action still renders, with its raw code', () => {
  assert.equal(changeActionLabel('escalate'), 'escalate');
  assert.equal(changeActionLabel(ACTION_APPLY), 'Apply change');
});

// -- the copy that answers "why can't I press Apply?" ----------------------------------------------

test('the stage summary names the missing signature', () => {
  assert.match(changeStageSummary(change({ status: 'PENDING_YARD' })), /yard/i);
  const finance = changeStageSummary(
    change({ status: 'PENDING_FINANCE', finance_required: true, finance_threshold_cents: 50000 }),
  );
  assert.match(finance, /finance/i);
  assert.match(finance, /\$500\.00/, 'the threshold in force must be NAMED, not implied');
});

test('the stage summary reads the STAMPED threshold, not a hardcoded $500', () => {
  // A branch configured at $250 must see $250. Re-deriving from today's configuration, or hardcoding
  // the default, would answer a different question when reading back an old change.
  const s = changeStageSummary(
    change({ status: 'PENDING_FINANCE', finance_required: true, finance_threshold_cents: 25000 }),
  );
  assert.match(s, /\$250\.00/);
});

test('the approved summary says applying re-runs reservations', () => {
  assert.match(changeStageSummary(change({ status: 'APPROVED' })), /reservation/i);
});

test('an unmapped change status still renders as a neutral chip', () => {
  assert.equal(changeStatusChip('SOMETHING_NEW'), 'chip');
  assert.equal(changeStatusChip('APPLIED'), 'chip green');
});

// -- the delta reads as what it is -------------------------------------------------------------------

test('a REMOVE says removed, never a to-zero quantity', () => {
  const s = describeDelta({
    seq: 1, op: 'REMOVE', line_no: 3, product_name: 'HPB Clear Stone', description: '',
    qty_base_before: 5, qty_base_after: 0, qty_delta_base: -5,
    unit_price_cents: 1000, line_delta_cents: -5000,
  });
  assert.match(s, /Remove/);
  assert.match(s, /line 3/);
});

test('a QTY change shows both quantities', () => {
  const s = describeDelta({
    seq: 1, op: 'QTY', line_no: 2, product_name: 'Sand', description: '',
    qty_base_before: 10, qty_base_after: 25, qty_delta_base: 15,
    unit_price_cents: 100, line_delta_cents: 1500,
  });
  assert.match(s, /10/);
  assert.match(s, /25/);
});

test('an ADD falls back to the description when the product name is absent', () => {
  const s = describeDelta({
    seq: 1, op: 'ADD', description: 'Cut-to-size service', qty_base_before: 0,
    qty_base_after: 2, qty_delta_base: 2, unit_price_cents: 5000, line_delta_cents: 10000,
  });
  assert.match(s, /Cut-to-size service/);
});

test('quantities lose their trailing-zero noise but keep real decimals', () => {
  assert.equal(formatQty(12.0), '12');
  assert.equal(formatQty(12.5), '12.5');
});

test('a caught exception renders as a sentence, not a refusal', () => {
  assert.equal(thrownMessage(new Error('boom')), 'boom');
  assert.equal(thrownMessage('not an error'), 'could not reach the server');
});

// -- the lane's additions: the wall words, the refusal mapping, the status pick --------------------

test('the walled actions name who may act, in the words the matrix names them', () => {
  // ROLE-APP-OBLIGATIONS 1: the shared Forbidden body names nobody; the app answers with plain
  // words naming the roles the default matrix puts on that route's census row.
  assert.match(wallWords(ACTION_YARD_ACK), /yard team/);
  assert.match(wallWords(ACTION_YARD_ACK), /yard manager/);
  assert.match(wallWords(ACTION_FINANCE_APPROVE), /financial controller/);
  assert.match(wallWords(ACTION_FINANCE_APPROVE), /general manager/);
  assert.match(wallWords(ACTION_APPLY), /sales manager/);
  assert.equal(wallWords(ACTION_CANCEL), '');
});

test('refusalWords keeps the server sentence verbatim except the bare Forbidden word', () => {
  assert.equal(
    refusalWords(ACTION_APPLY, 'order: this change is not approved'),
    'order: this change is not approved',
  );
  assert.equal(refusalWords(ACTION_APPLY, 'Forbidden'), wallWords(ACTION_APPLY));
  // a wall the map does not carry keeps the server's word: an honest refusal beats an invented
  // permission
  assert.equal(refusalWords(ACTION_CANCEL, 'Forbidden'), 'Forbidden');
});

test('statusIsPostPick mirrors the core ladder: the yard holds the goods', () => {
  assert.equal(statusIsPostPick('PICKING'), true);
  assert.equal(statusIsPostPick('READY'), true);
  assert.equal(statusIsPostPick('PARTIALLY_FULFILLED'), true);
  assert.equal(statusIsPostPick('CONFIRMED'), false);
  assert.equal(statusIsPostPick('DRAFT'), false);
  assert.equal(statusIsPostPick('FULFILLED'), false);
  assert.equal(statusIsPostPick('INVOICED'), false);
});
