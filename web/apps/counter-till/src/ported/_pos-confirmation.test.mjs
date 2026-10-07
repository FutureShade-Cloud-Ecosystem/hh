// The confirmation facts' own words (DIB-512's one-word-per-surface rule, and the visual read's
// G4): the tender row names what actually settled the sale. A split names its split and its legs;
// a plain tender keeps its own word. Node built-in test runner, .ts through the loader.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { saleConfirmation } from './_pos.ts';

// The visual read's G4: a cash plus card split confirmed as "TENDER Card", one leg's word for two
// legs' money. The split names itself and names its legs, in the tiles' own words, in the order
// the legs were submitted; repeated methods are said once.
test('a split confirmation names the split and its legs, never one leg alone', () => {
  const split = saleConfirmation({
    tender: 'CARD',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CASH', amount_cents: 1000 }, { method: 'CARD', amount_cents: 10300 }],
  });
  assert.equal(split.tenderLabel, 'Split: Cash $10.00 and Card $103.00');

  // The legs' own order is the submission's: card first reads card first.
  const reversed = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CARD', amount_cents: 6102 }, { method: 'CASH', amount_cents: 5198 }],
  });
  assert.equal(reversed.tenderLabel, 'Split: Card $61.02 and Cash $51.98');

  // Two legs on one rail are still a split: each leg is said with its own amount.
  const doubled = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CASH', amount_cents: 5000 }, { method: 'CASH', amount_cents: 6300 }],
  });
  assert.equal(doubled.tenderLabel, 'Split: Cash $50.00 and Cash $63.00');

  // Three legs read as a list, the last joined by and, in thousands with a comma.
  const three = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 123400,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CASH', amount_cents: 100000 }, { method: 'CARD', amount_cents: 20000 }, { method: 'ACCOUNT', amount_cents: 3400 }],
  });
  assert.equal(three.tenderLabel, 'Split: Cash $1,000.00, Card $200.00 and On account $34.00');

  // A leg with no stated amount (an answer from before the amounts rode along) keeps its word alone,
  // said once: no plausible zero is ever made up.
  const noAmounts = saleConfirmation({
    tender: 'CARD',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CASH' }, { method: 'CARD' }, { method: 'CARD' }],
  });
  assert.equal(noAmounts.tenderLabel, 'Split: Cash and Card');
});

// The plain tenders keep their own words: the split's words never leak onto a one-rail sale, and
// an account leg inside a split reads the account's own counter word.
test('a plain tender keeps its own confirmation word', () => {
  const cash = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
  });
  assert.equal(cash.tenderLabel, 'Cash');

  const account = saleConfirmation({
    tender: 'ACCOUNT',
    sale_type: 'COLLECT_NOW',
    total_cents: 10000,
  });
  assert.equal(account.tenderLabel, 'On account');

  // The split flag without legs (an answer that names the split but lists none) falls back to the
  // sale's own tender word rather than an empty "Split:".
  const bare = saleConfirmation({
    tender: 'CARD',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
  });
  assert.equal(bare.tenderLabel, 'Card');
});
