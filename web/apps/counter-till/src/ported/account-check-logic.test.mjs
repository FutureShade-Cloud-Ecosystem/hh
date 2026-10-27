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

test('a good standing account shows the server figures and no invented crew member', () => {
  const customer = {
    name: 'Palmera Landscapes',
    payment_terms_days: 30,
    credit_limit_cents: 1500000,
    open_balance_cents: 246000,
  };
  const res = evaluateAccountCheck(customer, { on_hold: false, past_due_cents: 0 });
  assert.ok(res !== null);
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.isBlocked, false);
  assert.equal(res.title, 'In good standing');
  assert.equal(res.lines[0], 'Available credit $12,540.00 · Terms 30 days.');
  assert.equal(res.lines[1], 'Palmera Landscapes may buy on this account. Every ticket needs a job and PO.');
  assert.equal(res.waysForward.length, 0);
});

test('a Palmera with no limit shows its true figure in words, never 12,540.00', () => {
  const res = evaluateAccountCheck({ name: 'Palmera Landscapes', credit_limit_cents: 0, open_balance_cents: 0 });
  assert.ok(res !== null);
  assert.equal(res.standing, 'good_standing');
  assert.ok(!res.lines.join(' ').includes('12,540'));
  assert.equal(
    res.lines[0],
    'Available credit none (the credit line is used up or not set) · No terms on file.'
  );
  assert.ok(!res.lines.join(' ').includes('Luis Ortega'));
});

test('a C09 account with no hold is not blocked', () => {
  const customer = { name: 'Harbour Drywall Inc', account_no: 'C09', credit_limit_cents: 200000 };
  assert.equal(isAccountBlocked(customer), false);
  assert.equal(evaluateAccountCheck(customer).standing, 'good_standing');
});

test('a customer named Bluffside Builders with no hold is not blocked', () => {
  const customer = { name: 'Bluffside Builders', account_no: 'C01', credit_limit_cents: 5000000, payment_terms_days: 30 };
  assert.equal(isAccountBlocked(customer), false);
  const res = evaluateAccountCheck(customer);
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.lines[0], 'Available credit $50,000.00 · Terms 30 days.');
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
    'Available credit $6,000.00 · Terms 15 days.'
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

test('a held account shows only the standing the server gave, with three ways forward', () => {
  const customer = {
    name: 'Bluffside Builders',
    credit_limit_cents: 2800000,
    open_balance_cents: 2482000,
  };
  const standing = {
    on_hold: true,
    past_due_cents: 1482000,
    oldest_bucket: '61 to 90',
    promise_text: 'Open promise: $10,000.00, not received.',
  };
  const res = evaluateAccountCheck(customer, standing);
  assert.equal(res.standing, 'blocked');
  assert.equal(res.title, 'On hold: past due 61 to 90 days');
  assert.deepEqual(res.lines, [
    'Available credit $3,180.00, but the account is on hold · Past due $14,820.00, 61 to 90 days.',
    'Open promise: $10,000.00, not received.',
    'Three ways forward:',
  ]);
  assert.equal(res.waysForward.length, 3);
  assert.equal(res.waysForward[0].title, 'Take a payment now');
  assert.equal(res.waysForward[1].title, 'Send a paylink');
  assert.ok(!res.waysForward[1].body.includes('555'));
  assert.equal(res.waysForward[2].title, 'Ask the credit manager');
});

test('a held account with a bare standing invents no past due, bucket, promise or credit', () => {
  const res = evaluateAccountCheck({ name: 'Bluffside Builders', on_hold: true, credit_limit_cents: 0, open_balance_cents: 0 });
  assert.equal(res.title, 'On hold');
  assert.equal(res.pastDueFormatted, null);
  assert.equal(res.bucketLabel, null);
  assert.equal(res.lines.length, 2);
  assert.equal(
    res.lines[0],
    'Available credit none (the credit line is used up or not set), but the account is on hold.'
  );
  assert.ok(!res.lines.join(' ').includes('14,820'));
});

test('a held account names a crew member only when one is passed in', () => {
  const res = evaluateAccountCheck({ name: 'X', on_hold: true }, null, { crewMemberName: 'Crew Person' });
  assert.equal(
    res.lines[res.lines.length - 1],
    'Crew Person is a listed crew member of X, so a charge to it will be refused.',
  );
});

// Round 3 (visual review): the withheld panel says the account is on hold exactly once, and the
// charge line is counter words, not server words.
test('a held account never says the account is on hold twice', () => {
  const customer = {
    name: 'Bluffside Builders',
    credit_limit_cents: 2800000,
    open_balance_cents: 2482000,
  };
  const res = evaluateAccountCheck(customer, { on_hold: true, past_due_cents: 0 });
  assert.equal(res.standing, 'blocked');
  const said = res.lines.join(' | ');
  assert.ok(said.includes('the account is on hold'), 'the hold is still named');
  assert.equal((said.match(/on hold/g) || []).length, 1, `the hold is named once: ${said}`);
  assert.equal(res.lines[res.lines.length - 1], 'Three ways forward:');
});

test('a withheld clerk reads no past due figure and no past due word anywhere on the check', () => {
  // The overdue amount is a credit figure like the limit, the balance and the available: a caller
  // the position withholds gets the hold words without it, word or figure, round 3 P1 3.
  const customer = { name: 'Fenwick Builders Ltd', account_no: 'C02', payment_terms_days: 30 };
  const res = evaluateAccountCheck(customer, { standing: 'clear', on_hold: false, past_due_cents: 0 });
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.title, 'No hold on this account');
  assert.equal(res.lines[0], 'Credit position not shown for your role · Terms 30 days.');
  assert.equal(res.lines[1], 'Fenwick Builders Ltd has no hold. The charge is checked when the sale rings.');
  assert.ok(!res.lines.join(' ').includes('$'));
});

test('a withheld clerk on a held account keeps the hold words and never sees the past due part', () => {
  const customer = { name: 'Harbour Drywall Inc', account_no: 'C09', payment_terms_days: 15 };
  const res = evaluateAccountCheck(customer, {
    standing: 'on_hold', on_hold: true, past_due_cents: 41500, oldest_bucket: '30',
  });
  assert.equal(res.standing, 'blocked');
  assert.equal(res.lines[0], 'Credit position not shown for your role, and the account is on hold.');
  assert.ok(!res.lines.join(' ').includes('Past due'), `no past due words: ${res.lines.join(' | ')}`);
  assert.ok(!res.lines.join(' ').includes('$'), `no figure at all: ${res.lines.join(' | ')}`);
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

test('a bucket that already says days is not doubled in the title', () => {
  const res = evaluateAccountCheck({ name: 'X', on_hold: true }, { oldest_bucket: '30' });
  assert.equal(res.title, 'On hold: past due 30 days');
  const res2 = evaluateAccountCheck({ name: 'X', on_hold: true }, { oldest_bucket: '90+ days' });
  assert.equal(res2.title, 'On hold: past due 90+ days');
});

test('the ways forward claim no send, no surcharge figure and no automatic lift', () => {
  const text = WAYS_FORWARD.map((w) => `${w.title} ${w.subtitle} ${w.body}`).join(' ');
  assert.ok(!/sent to|has been sent|was sent|2\.9|surcharge|lifts when|no fee/i.test(text), text);
  assert.ok(text.includes('Money Desk'));
  assert.ok(text.includes('released by hand'));
});

test('a terms-0 account reads no terms on file, never thirty days', () => {
  const res = evaluateAccountCheck({ name: 'Walk Up Retail', credit_limit_cents: 100000, payment_terms_days: 0 });
  assert.equal(res.termsDays, 0);
  assert.equal(res.lines[0], 'Available credit $1,000.00 · No terms on file.');
  assert.ok(!res.lines.join(' ').includes('30 days'));
  assert.ok(!res.lines.join(' ').includes('No open promise'));
});

test('a cash on demand account reads cash on demand, not terms or good standing credit words', () => {
  const res = evaluateAccountCheck(
    { name: 'Cash On Demand customer', credit_limit_cents: 500000, payment_terms_days: 0 },
    null,
    { cashOnDemand: true },
  );
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.lines[0], 'Cash on demand: pay before release.');
  assert.ok(!res.lines.join(' ').includes('Terms'));
  assert.ok(!res.lines.join(' ').includes('Available credit'));
  assert.ok(!res.lines.join(' ').includes('may buy on this account'));
});

test('an account with no available credit never reads may buy on this account', () => {
  const res = evaluateAccountCheck({
    name: 'At Limit Co',
    payment_terms_days: 30,
    credit_limit_cents: 100000,
    open_balance_cents: 100000,
  });
  assert.ok(!res.lines.join(' ').includes('may buy on this account'));
  assert.equal(res.lines[1], 'No credit is available, so a charge to this account will be refused.');
  assert.equal(res.title, 'No credit available');
});

// UX-97: the account check under the dealer's account position policy. Figures withheld: words, never
// a zero line and never a figure. (Folded in from the wrapper this round.)
import { positionWithheld, POSITION_WITHHELD_WORDS } from './account-check-logic.ts';

const withFigures = { name: 'A Builder', payment_terms_days: 30, credit_limit_cents: 1500000, open_balance_cents: 246000 };
const withheld = { name: 'A Builder', payment_terms_days: 30 };

test('with the figures present the check carries them and a figure-bearing customer is not withheld', () => {
  assert.equal(positionWithheld(withFigures), false);
  const res = evaluateAccountCheck(withFigures, { on_hold: false });
  assert.equal(res.lines[0], 'Available credit $12,540.00 · Terms 30 days.');
});

test('a withheld account on a cash on demand posture says pay before release and names no figure', () => {
  const res = evaluateAccountCheck(withheld, { on_hold: false }, { cashOnDemand: true });
  assert.equal(res.standing, 'good_standing');
  assert.ok(res.lines.join(' ').includes('Cash on demand: pay before release.'));
  assert.ok(!res.lines.join(' ').includes('$'));
  assert.ok(!res.lines.join(' ').includes('No credit available'));
});

test('a withheld account with no hold says so, names no figure and does not claim the credit line is zero', () => {
  assert.equal(positionWithheld(withheld), true);
  const res = evaluateAccountCheck(withheld, { on_hold: false });
  assert.equal(res.standing, 'good_standing');
  assert.equal(res.title, 'No hold on this account');
  const text = res.lines.join(' ');
  assert.ok(text.includes(POSITION_WITHHELD_WORDS));
  for (const bad of ['$', 'No credit available', 'used up', 'none (', 'may buy on this account']) {
    assert.ok(!text.includes(bad), `${bad} must not appear: ${text}`);
  }
});

test('a withheld account on hold is blocked from the server hold, with the ways forward and no money', () => {
  // Round 3 (P1 3): the overdue figure is a credit figure like the rest of the position, and the
  // standing read withholds it from this caller with the same field absence, so the panel carries
  // the hold words and never a number. The standing here is exactly the shape the core now answers.
  const res = evaluateAccountCheck(withheld, { standing: 'on_hold', on_hold: true });
  assert.equal(res.standing, 'blocked');
  assert.equal(res.waysForward.length, 3);
  assert.equal(res.title, 'On hold');
  assert.ok(res.lines[0].startsWith(POSITION_WITHHELD_WORDS));
  assert.ok(!res.lines[0].includes('Available credit'));
  assert.ok(!res.lines.join(' ').includes('$'), `no money anywhere: ${res.lines.join(' | ')}`);
});

test('a withheld account is not blocked from its name or number', () => {
  const res = evaluateAccountCheck({ ...withheld, name: 'Bluffside Builders', account_no: 'C09' }, { on_hold: false });
  assert.equal(res.isBlocked, false);
});
