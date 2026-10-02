// The pre-ring tax-and-total row's words (ringTaxWords in _pos.ts), one rule
// from decision log row 661: the ring states both figures, so the row names
// the ring, and an applied relief's engine-assessed rate is NAMED in words,
// never multiplied into a figure. The relief branch is the half a browser
// can only reach through the relief dialog's flow, so it is pinned here at
// the pure helper, where deleting the branch or multiplying its rate fails
// loudly.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ringTaxWords } from './_pos.ts';

test('no relief: the row names the ring and states nothing else', () => {
  assert.equal(ringTaxWords(null), 'added at the ring');
});

test('relief applied: the engine-assessed rate is named in words, never multiplied', () => {
  // The relief dialog's outcome rate, in basis points, spoken as a percent.
  assert.equal(ringTaxWords(1300), 'relief assessed at 13%: added at the ring');
  // A rate that is not a whole percent spells the fraction.
  assert.equal(ringTaxWords(650), 'relief assessed at 6.50%: added at the ring');
  // Words only: no figure of money, no percent sign doubled into a sum.
  const words = ringTaxWords(1300);
  assert.ok(!/CA\$/i.test(words), 'no money figure rides the relief words');
  assert.ok(!words.includes('+'), 'no addition rides the relief words');
});
