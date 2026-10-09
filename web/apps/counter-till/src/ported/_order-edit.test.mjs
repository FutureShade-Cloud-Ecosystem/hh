// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/_order-edit.test.mjs; adapted for HH.

// Regression for the pure order-edit helpers (_order-edit.ts), DIB-175. Runs on the Node built-in
// test runner (no deps), importing the .ts source through the loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test src/ported/_order-edit.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  canEdit,
  hasAnyEdit,
  validateQty,
  validateDate,
  fieldLabel,
  formatChangeValue,
  changeMoney,
  groupChanges,
  summariseGroup,
  changeActor,
  MAX_EDIT_QTY,
  EDIT_LINE_QTY,
  EDIT_REQUESTED_DATE,
} from './_order-edit.ts';

const view = (fields) => ({
  id: 'o1',
  status: 'CONFIRMED',
  branch_id: 'b1',
  legal_actions: [],
  editable_fields: fields,
});

// -- the client renders from the SERVER's matrix, never its own --------------------------------

test('canEdit offers only what the server listed', () => {
  const v = view([EDIT_LINE_QTY]);
  assert.equal(canEdit(v, EDIT_LINE_QTY), true);
  assert.equal(canEdit(v, EDIT_REQUESTED_DATE), false);
});

test('an absent editable_fields offers NOTHING (fail closed, never unlock everything)', () => {
  // An older server that predates DIB-175 sends no editable_fields. Defaulting to "offer everything"
  // would paint controls whose writes the server refuses: the panel must stay dark instead.
  assert.equal(canEdit({ id: 'o1', status: 'DRAFT', branch_id: 'b', legal_actions: [] }, EDIT_LINE_QTY), false);
  assert.equal(canEdit(null, EDIT_LINE_QTY), false);
  assert.equal(hasAnyEdit(null), false);
  assert.equal(hasAnyEdit(view([])), false);
  assert.equal(hasAnyEdit(view([EDIT_REQUESTED_DATE])), true);
});

test('a READY order offering only the date renders no line controls', () => {
  // The ratified matrix: line edits die at READY, the date lives on. The client learns this purely
  // from the server's answer.
  const ready = view([EDIT_REQUESTED_DATE]);
  assert.equal(canEdit(ready, EDIT_LINE_QTY), false);
  assert.equal(canEdit(ready, EDIT_REQUESTED_DATE), true);
});

// -- input validation mirrors the server, so the dialog refuses what the API would -------------

test('validateQty accepts a positive decimal', () => {
  assert.equal(validateQty('7'), null);
  assert.equal(validateQty('12.5'), null);
  assert.equal(validateQty('  3  '), null);
});

test('validateQty refuses zero, negative, blank and non-numeric', () => {
  assert.match(validateQty('0'), /more than zero/);
  assert.match(validateQty('-2'), /more than zero/);
  assert.match(validateQty(''), /Enter a quantity/);
  assert.match(validateQty('abc'), /must be a number/);
});

test('validateQty enforces the audit-safe bound (a 7-digit qty would be redacted from the trail)', () => {
  assert.equal(validateQty(String(MAX_EDIT_QTY)), null);
  assert.match(validateQty(String(MAX_EDIT_QTY + 1)), /or less/);
});

test('validateDate accepts YYYY-MM-DD and an empty value (which clears the date)', () => {
  assert.equal(validateDate('2026-09-15'), null);
  assert.equal(validateDate(''), null);
  assert.equal(validateDate('   '), null);
});

test('validateDate refuses a malformed or non-existent date', () => {
  assert.match(validateDate('15/09/2026'), /YYYY-MM-DD/);
  assert.match(validateDate('2026-9-1'), /YYYY-MM-DD/);
  assert.match(validateDate('2026-02-31'), /not a real date/);
  assert.match(validateDate('2026-13-01'), /not a real date/);
});

// -- rendering the trail -------------------------------------------------------------------------

test('money fields render as currency, other fields pass through', () => {
  assert.equal(formatChangeValue('line_total_cents', '700'), '$7.00');
  assert.equal(formatChangeValue('subtotal_cents', '123456'), '$1,234.56');
  assert.equal(formatChangeValue('qty_entered', '7.0000'), '7.0000');
  assert.equal(formatChangeValue('requested_date', '2026-09-15'), '2026-09-15');
});

test('an absent side of the pair renders as (none): a creation has no before, a removal no after', () => {
  assert.equal(formatChangeValue('qty_entered', undefined), '(none)');
  assert.equal(formatChangeValue('line_total_cents', ''), '(none)');
});

test('changeMoney keeps the sign so a removal reads as a removal', () => {
  assert.equal(changeMoney(60000), '$600.00');
  assert.equal(changeMoney(-4000), '-$40.00');
  assert.equal(changeMoney(0), '$0.00');
});

test('fieldLabel names known columns and falls back to the raw code', () => {
  assert.equal(fieldLabel('qty_entered'), 'Quantity');
  assert.equal(fieldLabel('line_removed'), 'Line removed');
  assert.equal(fieldLabel('some_new_column'), 'some_new_column');
});

test('changeActor prefers a display name, then a short id, then system', () => {
  assert.equal(changeActor({ actor_name: 'Sales manager (demo)', actor_id: 'abcdef1234' }), 'Sales manager (demo)');
  assert.equal(changeActor({ actor_id: 'abcdef1234' }), 'abcdef12');
  assert.equal(changeActor({}), 'system');
});

// -- one operator action reads as ONE history entry ----------------------------------------------

const row = (field, request_id, before, after) => ({
  field,
  line_no: 1,
  before,
  after,
  request_id,
  occurred_at: '2026-07-29T12:00:00Z',
});

test('rows sharing a request id fold into one group: a qty edit is one action, not five', () => {
  const groups = groupChanges([
    row('qty_entered', 'req-1', '2.0000', '7.0000'),
    row('qty_base', 'req-1', '2.0000', '7.0000'),
    row('line_total_cents', 'req-1', '200', '700'),
    row('subtotal_cents', 'req-1', undefined, '700'),
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].length, 4);
});

test('separate actions stay separate, in the order they occurred', () => {
  const groups = groupChanges([
    row('qty_entered', 'req-1', '2.0000', '7.0000'),
    row('requested_date', 'req-2', '2026-08-01', '2026-09-15'),
  ]);
  assert.equal(groups.length, 2);
  assert.equal(groups[0][0].field, 'qty_entered');
  assert.equal(groups[1][0].field, 'requested_date');
});

test('rows without a request id each stand alone (we cannot prove they share an action)', () => {
  const groups = groupChanges([
    row('qty_entered', undefined, '2', '7'),
    row('qty_base', undefined, '2', '7'),
  ]);
  assert.equal(groups.length, 2);
});

test('summariseGroup leads with the operator action, not a derived column', () => {
  const group = [
    row('subtotal_cents', 'req-1', undefined, '700'),
    row('qty_base', 'req-1', '2.0000', '7.0000'),
    row('qty_entered', 'req-1', '2.0000', '7.0000'),
  ];
  assert.equal(summariseGroup(group).field, 'qty_entered');
});

test('summariseGroup surfaces an add/remove ahead of everything else', () => {
  assert.equal(
    summariseGroup([row('subtotal_cents', 'r', undefined, '0'), row('line_removed', 'r', 'prod-1', undefined)]).field,
    'line_removed',
  );
  assert.equal(
    summariseGroup([row('qty_entered', 'r', undefined, '3'), row('line_added', 'r', undefined, 'prod-1')]).field,
    'line_added',
  );
});

test('groupChanges tolerates an empty history', () => {
  assert.deepEqual(groupChanges([]), []);
  assert.equal(summariseGroup([]), undefined);
});
