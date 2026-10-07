// The sale-preview helpers (row 985): the request's shape and fingerprint, the answer's reading, and
// the split refusal's words, pinned at the pure module so each branch fails loudly when it moves.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { previewBody, previewKey, readPreview, splitMismatchWords } from './_pos-preview.ts';

const facts = (over = {}) => ({
  branchId: 'b1',
  customerId: '',
  saleType: 'COLLECT_NOW',
  fulfilment: 'PICKUP',
  lines: [{ productId: 'p1', qty: 2, uom: 'SF' }],
  ...over,
});

test('an empty ticket asks nothing: no body and an empty key', () => {
  assert.equal(previewBody(facts({ lines: [] })), null);
  assert.equal(previewKey(null), '');
});

test('the body carries the sale facts and no tender; fulfilment rides a COD sale only', () => {
  const body = previewBody(facts());
  assert.deepEqual(body.lines, [{ product_id: 'p1', qty_entered: 2, uom_entered: 'SF' }]);
  assert.equal(body.fulfilment, undefined);
  assert.equal('tender' in body, false);
  assert.equal(previewBody(facts({ saleType: 'COD' })).fulfilment, 'PICKUP');
});

test('the key changes with the lines, the customer and the fulfilment, and only with them', () => {
  const base = previewKey(previewBody(facts()));
  assert.equal(previewKey(previewBody(facts())), base);
  assert.notEqual(previewKey(previewBody(facts({ lines: [{ productId: 'p1', qty: 3, uom: 'SF' }] }))), base);
  assert.notEqual(previewKey(previewBody(facts({ customerId: 'c1' }))), base);
  assert.notEqual(previewKey(previewBody(facts({ saleType: 'COD' }))), base);
});

test('an answer is three whole cent figures or it is not an answer', () => {
  assert.deepEqual(readPreview({ subtotal_cents: 400, tax_cents: 52, total_cents: 452 }), {
    kind: 'ready',
    subtotalCents: 400,
    taxCents: 52,
    totalCents: 452,
    discountCents: 0,
    saleDiscountCents: 0,
    lines: [],
  });
  for (const bad of [null, 'x', {}, { subtotal_cents: 400, tax_cents: 52 }, { subtotal_cents: 400, tax_cents: 52.5, total_cents: 452 }, { subtotal_cents: '400', tax_cents: 52, total_cents: 452 }]) {
    assert.deepEqual(readPreview(bad), { kind: 'unavailable' });
  }
});

test('the split refusal reads from its fields, then the server message, then a fixed sentence', () => {
  const money = (c) => `$${(c / 100).toFixed(2)}`;
  assert.equal(
    splitMismatchWords({ tendered_cents: 26600, total_cents: 30058, message: 'something else entirely' }, money),
    'The tenders add up to $266.00, but the sale total is $300.58. Change the legs so they match.',
  );
  assert.equal(splitMismatchWords({ message: 'The server says so.' }, money), 'The server says so.');
  assert.match(splitMismatchWords({}, money), /do not add up to the sale total/);
});
