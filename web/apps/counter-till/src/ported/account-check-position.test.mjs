// UX-97: the account check under the dealer's account position policy. Figures present: the check is
// evaluateAccountCheck exactly. Figures withheld: words, never a zero line and never a figure.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluateAccountCheck } from './account-check-logic.ts';
import {
  evaluateAccountCheckUnderPolicy,
  positionWithheld,
  POSITION_WITHHELD_WORDS,
} from './account-check-position.ts';

const withFigures = { name: 'A Builder', payment_terms_days: 30, credit_limit_cents: 1500000, open_balance_cents: 246000 };
const withheld = { name: 'A Builder', payment_terms_days: 30 };

test('with the figures present the check is the unchanged one', () => {
  assert.deepEqual(
    evaluateAccountCheckUnderPolicy(withFigures, { on_hold: false }),
    evaluateAccountCheck(withFigures, { on_hold: false }),
  );
  assert.equal(positionWithheld(withFigures), false);
  assert.equal(evaluateAccountCheckUnderPolicy(null), null);
});

test('a withheld account with no hold says so, names no figure and does not claim the credit line is zero', () => {
  assert.equal(positionWithheld(withheld), true);
  const res = evaluateAccountCheckUnderPolicy(withheld, { on_hold: false });
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.title, 'No hold on this account');
  const text = res.lines.join(' ');
  assert.ok(text.includes(POSITION_WITHHELD_WORDS));
  for (const bad of ['$', 'No credit available', 'used up', 'none (', 'may buy on this account']) {
    assert.ok(!text.includes(bad), `${bad} must not appear: ${text}`);
  }
});

test('a withheld account on hold is blocked from the server hold, with the ways forward and no figure', () => {
  const res = evaluateAccountCheckUnderPolicy(withheld, { on_hold: true, past_due_cents: 1482000, oldest_bucket: '61 to 90' });
  assert.equal(res.standing, 'blocked');
  assert.equal(res.waysForward.length, 3);
  assert.equal(res.title, 'On hold: past due 61 to 90 days');
  assert.ok(res.lines[0].startsWith(POSITION_WITHHELD_WORDS));
  assert.ok(!res.lines[0].includes('Available credit'));
  assert.ok(res.lines[0].includes('$14,820.00'), 'the past due figure is the standing read, not the position');
});

test('a withheld account is not blocked from its name or number', () => {
  const res = evaluateAccountCheckUnderPolicy({ ...withheld, name: 'Bluffside Builders', account_no: 'C09' }, { on_hold: false });
  assert.equal(res.isBlocked, false);
});
