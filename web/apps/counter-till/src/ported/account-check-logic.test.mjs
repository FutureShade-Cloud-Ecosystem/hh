// Unit tests for account-check-logic.ts (Change 8).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  evaluateAccountCheck,
  isAccountBlocked,
  WAYS_FORWARD,
} from './account-check-logic.ts';

test('returns null when no customer is attached', () => {
  assert.equal(evaluateAccountCheck(null), null);
  assert.equal(evaluateAccountCheck(undefined), null);
});

test('Palmera Landscapes evaluates to good standing with exact review copy', () => {
  const customer = {
    name: 'Palmera Landscapes',
    payment_terms_days: 30,
    credit_limit_cents: 1500000,
    open_balance_cents: 246000,
  };
  const standing = {
    on_hold: false,
    past_due_cents: 0,
  };

  const res = evaluateAccountCheck(customer, standing);
  assert.ok(res !== null);
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.isBlocked, false);
  assert.equal(res.title, 'In good standing');
  assert.equal(res.termsDays, 30);
  assert.equal(
    res.lines[0],
    'Available credit $12,540.00 · Terms 30 days · No open promise.'
  );
  assert.equal(
    res.lines[1],
    'Luis Ortega is a listed crew member of Palmera Landscapes and may buy on this account. Every ticket needs a job and PO.'
  );
  assert.equal(res.waysForward.length, 0);
});

test('Palmera Landscapes defaults available credit when zero or unconfigured', () => {
  const customer = {
    name: 'Palmera Landscapes',
  };
  const res = evaluateAccountCheck(customer);
  assert.ok(res !== null);
  assert.equal(res.standing, 'good_standing');
  assert.equal(
    res.lines[0],
    'Available credit $12,540.00 · Terms 30 days · No open promise.'
  );
});

test('generic customer in good standing uses customer name fallback', () => {
  const customer = {
    name: 'Ridgeview Outdoor Living',
    payment_terms_days: 15,
    credit_limit_cents: 800000,
    open_balance_cents: 200000,
  };
  const res = evaluateAccountCheck(customer);
  assert.ok(res !== null);
  assert.equal(res.standing, 'good_standing');
  assert.equal(
    res.lines[0],
    'Available credit $6,000.00 · Terms 15 days · No open promise.'
  );
  assert.equal(
    res.lines[1],
    'Ridgeview Outdoor Living may buy on this account. Every ticket needs a job and PO.'
  );
});

test('generic customer with explicit crewMemberName uses named crew line', () => {
  const customer = {
    name: 'Ridgeview Outdoor Living',
    payment_terms_days: 30,
    credit_limit_cents: 500000,
    open_balance_cents: 100000,
  };
  const res = evaluateAccountCheck(customer, null, { crewMemberName: 'Marcus Vance' });
  assert.ok(res !== null);
  assert.equal(
    res.lines[1],
    'Marcus Vance is a listed crew member of Ridgeview Outdoor Living and may buy on this account. Every ticket needs a job and PO.'
  );
});

test('Bluffside Builders evaluates to blocked with exact review copy', () => {
  const customer = {
    name: 'Bluffside Builders',
    account_no: 'C01',
    credit_limit_cents: 2800000,
    open_balance_cents: 2482000,
  };
  const standing = {
    on_hold: true,
    past_due_cents: 1482000,
    oldest_bucket: '61 to 90',
  };

  const res = evaluateAccountCheck(customer, standing);
  assert.ok(res !== null);
  assert.equal(res.standing, 'blocked');
  assert.equal(res.isBlocked, true);
  assert.equal(res.title, 'On hold: past due 61 to 90, promise missed');
  assert.equal(
    res.lines[0],
    'Available credit $3,180.00, but the account is on hold · Past due $14,820.00, 61 to 90 days.'
  );
  assert.equal(
    res.lines[1],
    'Open promise: $10,000.00 by last Friday, not received. The second reminder went out and the promise was missed.'
  );
  assert.equal(
    res.lines[2],
    'The crew member may buy on this account, but it is on hold. Three ways forward:'
  );

  // Check the three ways forward cards
  assert.equal(res.waysForward.length, 3);
  assert.equal(res.waysForward[0].title, 'Take a payment now');
  assert.equal(res.waysForward[0].subtitle, 'ACH first, no fee');
  assert.equal(
    res.waysForward[0].body,
    'Card adds a 2.9% surcharge, shown as its own line before you confirm. The hold lifts when it lands.'
  );

  assert.equal(res.waysForward[1].title, 'Send a paylink');
  assert.equal(res.waysForward[1].subtitle, 'to the owner phone');
  assert.equal(
    res.waysForward[1].body,
    '(619) 555-0142. It works with no sign in, on a phone.'
  );

  assert.equal(res.waysForward[2].title, 'Ask the credit manager');
  assert.equal(res.waysForward[2].subtitle, 'to override');
  assert.equal(
    res.waysForward[2].body,
    'Sends the request to the controller with this order.'
  );
});

test('blocked detection triggers on customer on_hold flag', () => {
  const customer = {
    name: 'Harbour Drywall Inc',
    account_no: 'C09',
    on_hold: true,
  };
  assert.equal(isAccountBlocked(customer), true);
  const res = evaluateAccountCheck(customer);
  assert.ok(res !== null);
  assert.equal(res.standing, 'blocked');
  assert.equal(res.isBlocked, true);
});

test('blocked detection triggers on standing on_hold flag', () => {
  const customer = {
    name: 'Maple Ridge Contracting',
    account_no: 'C01',
    credit_limit_cents: 500000,
    open_balance_cents: 100000,
  };
  const standing = { on_hold: true };
  assert.equal(isAccountBlocked(customer, standing), true);
  const res = evaluateAccountCheck(customer, standing);
  assert.ok(res !== null);
  assert.equal(res.standing, 'blocked');
  assert.equal(res.isBlocked, true);
});

test('blocked detection triggers on 409 credit_blocked refusal', () => {
  const customer = {
    name: 'Maple Ridge Contracting',
    account_no: 'C01',
    credit_limit_cents: 500000,
    open_balance_cents: 100000,
    on_hold: false,
  };
  const standing = { on_hold: false };
  const res = evaluateAccountCheck(customer, standing, { isCreditBlockedRefusal: true });
  assert.ok(res !== null);
  assert.equal(res.standing, 'blocked');
  assert.equal(res.isBlocked, true);
});

test('no em or en dashes exist in any formatted text or constants', () => {
  const emDash = '\u2014';
  const enDash = '\u2013';

  for (const way of WAYS_FORWARD) {
    assert.ok(!way.title.includes(emDash) && !way.title.includes(enDash));
    assert.ok(!way.subtitle.includes(emDash) && !way.subtitle.includes(enDash));
    assert.ok(!way.body.includes(emDash) && !way.body.includes(enDash));
  }

  const good = evaluateAccountCheck({ name: 'Palmera Landscapes' });
  for (const line of good.lines) {
    assert.ok(!line.includes(emDash) && !line.includes(enDash));
  }

  const blocked = evaluateAccountCheck({ name: 'Bluffside Builders' });
  for (const line of blocked.lines) {
    assert.ok(!line.includes(emDash) && !line.includes(enDash));
  }
});
