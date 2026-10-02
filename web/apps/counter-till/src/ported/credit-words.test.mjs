// The credit gate's refusal words (creditBlockMessage in _pos.ts), one rule
// for both ring arms: the sentence follows the policy the gate named, because
// the gate answers one error word for refusals that are not the same refusal.
// The account sentence is true for a hold and for an over-the-limit refusal
// (the comprehensive train review's reviewed words); the same sentence on a
// walk-in COD refusal was false in every clause, which is the defect the COD
// arm exists for.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { creditBlockMessage } from './_pos.ts';

const COD_WALK_IN = 'COD needs a customer account: pick one, or ring the sale as Collect now.';
const ACCOUNT_SENTENCE =
  'This account cannot carry this sale on account: the credit gate refused it (over the limit, or on a hold). Cash or card still works, and accounts receivable can release or override a hold.';

test('a walk-in COD refusal names COD, the missing account, and the two ways out', () => {
  const words = creditBlockMessage('COD_CONSTRAINT', false);
  assert.equal(words, COD_WALK_IN);
  // The three facts the sentence owes the clerk: what the sale is, what it
  // lacks, and both ways forward.
  assert.match(words, /COD/);
  assert.match(words, /customer account/);
  assert.match(words, /Collect now/);
  // The false account sentence must not ride this refusal: no account is on
  // the ticket, nothing is on account, and Cash is the chosen tender.
  assert.ok(!words.includes('This account cannot carry this sale on account'));
  assert.ok(!words.includes('Cash or card still works'));
});

test('a hold refusal with the account on the ticket keeps the reviewed sentence', () => {
  // The stub and the core both answer COD_CONSTRAINT for an on-hold ACCOUNT
  // sale, so the policy alone does not separate the two refusals: the account
  // on the ticket does.
  assert.equal(creditBlockMessage('COD_CONSTRAINT', true), ACCOUNT_SENTENCE);
});

test('an over-the-limit refusal keeps the reviewed sentence', () => {
  assert.equal(creditBlockMessage('CREDIT_LIMIT_EXCEEDED', true), ACCOUNT_SENTENCE);
});

test('a credit_blocked answer with no policy word keeps the reviewed sentence', () => {
  // The floor: a policy-less credit_blocked (an older server, a proxy strip)
  // degrades to the sentence that is true for the refusals that name an
  // account, never to a blank.
  assert.equal(creditBlockMessage(undefined, true), ACCOUNT_SENTENCE);
});
