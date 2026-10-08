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

test('the sentence under the lines follows the shape', () => {
  assert.equal(
    depositSentence('DEPOSIT_NOW_REST_ON_ACCOUNT'),
    'A credit customer: the rest rides their account and is paid from the statement.',
  );
  assert.equal(
    depositSentence('DEPOSIT_NOW_REST_DUE_LATER'),
    'Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full.',
  );
});

test('the button names the deposit in dollars', () => {
  assert.equal(depositButtonWords(50000, money), 'Ring sale with $500.00 deposit');
  assert.equal(depositButtonWords(0, money), 'Enter the deposit');
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
