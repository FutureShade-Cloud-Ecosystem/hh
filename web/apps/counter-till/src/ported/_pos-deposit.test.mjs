// Item 9.6: the deposit shape at the payment step, pinned at the pure module. The menu and line
// two's words come from the preview's payment_options; the till decides no money rule.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  depositOption,
  depositOffered,
  depositRingFacts,
  depositSentence,
  depositRefusalWords,
  depositButtonWords,
  cashOnDemandPosture,
  supplierDepositLine,
  supplierDepositRefusalWords,
  supplierDepositShown,
} from './_pos-deposit.ts';
import { readPreview } from './_pos-preview.ts';

const credit = [
  { shape: 'PAY_IN_FULL_NOW' },
  { shape: 'ALL_ON_ACCOUNT' },
  { shape: 'DEPOSIT_NOW_REST_ON_ACCOUNT', lineTwoWords: 'On account' },
];
const cod = [{ shape: 'PAY_IN_FULL_NOW' }, { shape: 'DEPOSIT_NOW_REST_DUE_LATER', lineTwoWords: 'Balance due later' }];
const money = (c) => `$${(c / 100).toFixed(2)}`;

test('the preview answer carries the options, read whole or not at all', () => {
  const answer = {
    subtotal_cents: 400,
    tax_cents: 52,
    total_cents: 452,
    payment_options: [
      { shape: 'PAY_IN_FULL_NOW' },
      { shape: 'DEPOSIT_NOW_REST_DUE_LATER', line_two_words: 'Balance due later' },
    ],
  };
  assert.deepEqual(readPreview(answer).paymentOptions, cod);
  assert.deepEqual(readPreview({ ...answer, payment_options: undefined }).paymentOptions, []);
  assert.deepEqual(readPreview({ ...answer, payment_options: [{ nope: 1 }, 'x'] }).paymentOptions, []);
});

test('the deposit option is whichever deposit shape the server offered', () => {
  assert.equal(depositOption(credit).lineTwoWords, 'On account');
  assert.equal(depositOption(cod).shape, 'DEPOSIT_NOW_REST_DUE_LATER');
  assert.equal(depositOption([{ shape: 'PAY_IN_FULL_NOW' }]), null);
});

test('a deposit is offered only where the goods wait, and only when the server offers it', () => {
  assert.equal(depositOffered('COLLECT_NOW', credit), false);
  assert.equal(depositOffered('COLLECT_LATER', credit), true);
  assert.equal(depositOffered('DELIVERED', cod), true);
  assert.equal(depositOffered('COD', cod), true);
  assert.equal(depositOffered('COD', credit), false);
  assert.equal(depositOffered('COLLECT_LATER', [{ shape: 'PAY_IN_FULL_NOW' }]), false);
});

test('the sentence under the lines is the server\'s, by reason, and absent when the server sent none', () => {
  const sentence = 'This account cannot carry this sale on account: pay in full now, or take a deposit with the balance due later.';
  const refused = [{ shape: 'PAY_IN_FULL_NOW' }, { shape: 'DEPOSIT_NOW_REST_DUE_LATER', lineTwoWords: 'Balance due later', lineTwoSentence: sentence }];
  assert.equal(depositSentence(depositOption(refused)), sentence);
  assert.equal(depositSentence(depositOption(cod)), '');
  assert.equal(depositSentence(null), '');
  const answer = { subtotal_cents: 1, tax_cents: 0, total_cents: 1, payment_options: [{ shape: 'DEPOSIT_NOW_REST_DUE_LATER', line_two_words: 'Balance due later', line_two_sentence: sentence }] };
  assert.equal(readPreview(answer).paymentOptions[0].lineTwoSentence, sentence);
});

test('the button names the deposit in dollars', () => {
  assert.equal(depositButtonWords(50000, money), 'Ring sale with $500.00 deposit');
  assert.equal(depositButtonWords(0, money), 'Enter the deposit');
});

// Round 16 (FROM-PRINCIPAL 37 ii): the Cash On Demand account's card line. The demo world's C10
// (zero day terms with the 500000 line the core's Cash On Demand rule keys on) used to read
// "Credit available $5,000.00", a figure with no meaning for an account that pays before release;
// the card says the posture instead, and a credit account keeps its figure.
test('the Cash On Demand posture replaces the credit figure only for the Cash On Demand account', () => {
  assert.equal(cashOnDemandPosture(cod, 0, 500000), 'Cash On Demand: pay before release');
  // The branch switch off (pay in full now only) does not make the figure honest again: the
  // account is still the pay before release account.
  assert.equal(cashOnDemandPosture([{ shape: 'PAY_IN_FULL_NOW' }], 0, 500000), 'Cash On Demand: pay before release');
  // Before the preview answers, the same account: the posture, never the figure.
  assert.equal(cashOnDemandPosture([], 0, 500000), 'Cash On Demand: pay before release');
  // A credit account keeps its figure (the server offers the on account shape).
  assert.equal(cashOnDemandPosture(credit, 30, 500000), null);
  // A held credit account is offered the pay before release shapes, and it is still a credit
  // account: its figure shows and the hold tag carries the hold.
  assert.equal(cashOnDemandPosture(cod, 15, 200000), null);
  // An on account shape offered means the account transacts as credit, whatever the terms read.
  assert.equal(cashOnDemandPosture(credit, 0, 500000), null);
  // No line: the card's "No credit line" line, not the posture.
  assert.equal(cashOnDemandPosture(cod, 0, 0), null);
});

test('the ring facts: credit rides ACCOUNT, pay before release rides COD with the waiting fulfilment', () => {
  assert.deepEqual(depositRingFacts('DEPOSIT_NOW_REST_ON_ACCOUNT', 'COLLECT_LATER', 'PICKUP'), {
    tender: 'ACCOUNT',
    saleType: 'COLLECT_LATER',
    fulfilment: undefined,
  });
  assert.deepEqual(depositRingFacts('DEPOSIT_NOW_REST_DUE_LATER', 'COLLECT_LATER', 'PICKUP'), {
    tender: 'CASH',
    saleType: 'COD',
    fulfilment: 'WILL_CALL',
  });
  assert.deepEqual(depositRingFacts('DEPOSIT_NOW_REST_DUE_LATER', 'DELIVERED', 'PICKUP'), {
    tender: 'CASH',
    saleType: 'COD',
    fulfilment: 'DELIVERY',
  });
  assert.deepEqual(depositRingFacts('DEPOSIT_NOW_REST_DUE_LATER', 'COD', 'DELIVERY'), {
    tender: 'CASH',
    saleType: 'COD',
    fulfilment: 'DELIVERY',
  });
});

test('refusals read from the server message, else from the fields, never cents or a code', () => {
  assert.equal(depositRefusalWords({ status: 422, error: 'deposit_not_collect_now', message: 'Take the whole $10.00 now.' }, money), 'Take the whole $10.00 now.');
  assert.equal(
    depositRefusalWords({ status: 422, error: 'deposit_not_collect_now', total_cents: 1000, deposit_cents: 200 }, money),
    'A collect now sale is paid in full: take the whole $10.00 now, or choose will call or delivery to leave a deposit.',
  );
  const words = depositRefusalWords({ status: 422, error: 'on_account_not_available', amount_cents: 5000 }, money);
  assert.match(words, /\$50\.00/);
  assert.doesNotMatch(words, /on_account|5000/);
  assert.equal(depositRefusalWords({ status: 409, error: 'nope' }, money), null);
});

// Item 9 review P3-7: the sale type refusal's words come from the server's
// message (the ruling carries no figures), with the fixed fallback.
test('depositRefusalWords passes the sale type refusal through', () => {
  assert.equal(
    depositRefusalWords({ status: 422, error: 'cod_not_for_terms_account', message: 'The server sentence.' }, money),
    'The server sentence.',
  );
  assert.equal(
    depositRefusalWords({ status: 422, error: 'cod_not_for_terms_account' }, money),
    'This account pays on terms, so its sale is not pay before release. Ring it on account or paid in full.',
  );
  assert.equal(depositRefusalWords({ status: 422, error: 'other' }, money), null);
});

// -- item 9.5: the supplier's demanded deposit, billed to the account --

test('the supplier deposit control shows only where the server allows and the ring may carry it', () => {
  const allowed = { allowed: true, words: 'Supplier deposit, billed to the account' };
  const refused = { allowed: false, words: null };
  assert.equal(supplierDepositShown(allowed, 'ACCOUNT', false), true);
  assert.equal(supplierDepositShown(allowed, 'CASH', false), false);
  assert.equal(supplierDepositShown(allowed, 'ACCOUNT', true), false);
  assert.equal(supplierDepositShown(refused, 'ACCOUNT', false), false);
});

test('the supplier deposit line says what the Summary and receipt print', () => {
  assert.equal(supplierDepositLine(25000, money), 'Supplier deposit, billed to the account: $250.00');
});

test('the supplier deposit refusals read the message first, the fixed fallback second', () => {
  assert.equal(
    supplierDepositRefusalWords({ status: 422, error: 'supplier_deposit_not_credit', message: 'The server words.' }),
    'The server words.',
  );
  assert.notEqual(
    supplierDepositRefusalWords({ status: 422, error: 'supplier_deposit_not_credit' }),
    null,
  );
  assert.notEqual(
    supplierDepositRefusalWords({ status: 422, error: 'supplier_deposit_over_pretax', message: '' }),
    null,
  );
  assert.equal(supplierDepositRefusalWords({ status: 422, error: 'other' }), null);
});

test('the preview carries the supplier deposit allowance, whole or not at all', () => {
  const answer = {
    subtotal_cents: 400,
    tax_cents: 52,
    total_cents: 452,
    payment_options: [],
    supplier_deposit: { allowed: true, words: 'Supplier deposit, billed to the account' },
  };
  assert.deepEqual(readPreview(answer).supplierDeposit, {
    allowed: true,
    words: 'Supplier deposit, billed to the account',
  });
  const none = { ...answer, supplier_deposit: { allowed: false } };
  assert.deepEqual(readPreview(none).supplierDeposit, { allowed: false, words: null });
  const absent = { ...answer, supplier_deposit: undefined };
  assert.deepEqual(readPreview(absent).supplierDeposit, { allowed: false, words: null });
});

// Run D's finding: the deposit's money bound is a problem body now (deposit_out_of_range, integer
// cents fields, a message in dollars). The server's message wins; the fields word it only when the
// message is missing; nothing else changes.
test('depositRefusalWords words the money bound: message first, then the fields in dollars', () => {
  const money = (c) => `$${(c / 100).toFixed(2)}`;
  assert.equal(
    depositRefusalWords(
      {
        status: 422,
        error: 'deposit_out_of_range',
        deposit_cents: 500,
        total_cents: 452,
        message: 'The deposit must be more than $0.00 and less than the sale total of $4.52.',
      },
      money,
    ),
    'The deposit must be more than $0.00 and less than the sale total of $4.52.',
  );
  assert.equal(
    depositRefusalWords({ status: 422, error: 'deposit_out_of_range', deposit_cents: 500, total_cents: 452 }, money),
    'The deposit must be more than $0.00 and less than the sale total of $4.52.',
  );
  assert.equal(
    depositRefusalWords({ status: 422, error: 'deposit_out_of_range' }, money),
    'The deposit must be more than zero and less than the sale total.',
  );
  assert.equal(depositRefusalWords({ status: 422, error: 'other' }, money), null);
});
