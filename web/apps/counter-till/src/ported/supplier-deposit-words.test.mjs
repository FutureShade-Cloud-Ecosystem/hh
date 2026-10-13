// The cancelled special order's supplier deposit rule (the counter settings
// screen's select and the history share one spelling): both dispositions have
// words, the rule has a label, and the history renders the value through them.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  POS_HISTORY_WORDS,
  POS_RULE_WORDS,
  SUPPLIER_DEPOSIT_CANCEL_WORDS,
} from './pos-counter-settings-logic.ts';

test('both dispositions have words and no other value does', () => {
  assert.deepEqual(Object.keys(SUPPLIER_DEPOSIT_CANCEL_WORDS).sort(), ['CREDIT_BACK', 'KEPT']);
  assert.match(SUPPLIER_DEPOSIT_CANCEL_WORDS.CREDIT_BACK, /customer's account/);
  assert.match(SUPPLIER_DEPOSIT_CANCEL_WORDS.KEPT, /dealer/);
});

test('the rule is labelled and the history renders its values in the same words', () => {
  assert.match(POS_RULE_WORDS.supplier_deposit_cancel, /supplier deposit/);
  assert.equal(POS_HISTORY_WORDS.value.supplier_deposit_cancel, SUPPLIER_DEPOSIT_CANCEL_WORDS);
});
