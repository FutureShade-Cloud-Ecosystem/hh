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
    tenders: [{ method: 'CASH' }, { method: 'CARD' }],
  });
  assert.equal(split.tenderLabel, 'Split: Cash and Card');

  // The legs' own order is the submission's: card first reads card first.
  const reversed = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CARD' }, { method: 'CASH' }],
  });
  assert.equal(reversed.tenderLabel, 'Split: Card and Cash');

  // Two legs on one rail are still a split, and the rail is said once.
  const doubled = saleConfirmation({
    tender: 'CASH',
    sale_type: 'COLLECT_NOW',
    total_cents: 11300,
    payment_id: 'pay-1',
    split_tender: true,
    tenders: [{ method: 'CASH' }, { method: 'CASH' }],
  });
  assert.equal(doubled.tenderLabel, 'Split: Cash');
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
