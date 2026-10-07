// The counter discount's pure rules (row 1047 item 3), pinned at the DOM-free modules so each
// branch fails loudly when it moves: what the person typed, the words for it, the wire shape, the
// preview's body and answer, the merge guard and the park guard.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDiscountEntry,
  discountWords,
  discountWire,
  ticketCarriesDiscount,
  discountFromLine,
} from './_pos-discount.ts';
import { previewBody, previewKey, readPreview } from './_pos-preview.ts';
import { lineMergeBlockers, LINE_FIELD_RULES, PRICE_ANSWER_FIELDS, applyPriceAnswer } from './_pos.ts';

const approver = { approvedBy: 'u-mgr', approvedByName: 'Sales manager' };

test('a percent is read through the decimal string into basis points', () => {
  assert.deepEqual(parseDiscountEntry('percent', '10'), { ok: true, discount: { percentBps: 1000 } });
  assert.deepEqual(parseDiscountEntry('percent', '12.5'), { ok: true, discount: { percentBps: 1250 } });
  assert.deepEqual(parseDiscountEntry('percent', '0.5'), { ok: true, discount: { percentBps: 50 } });
  assert.deepEqual(parseDiscountEntry('percent', ' 100 '), { ok: true, discount: { percentBps: 10000 } });
  for (const bad of ['', '0', '0.00', '100.01', '101', '-5', '10.123', 'abc', '5%', '.5', '1,5']) {
    const r = parseDiscountEntry('percent', bad);
    assert.equal(r.ok, false, `"${bad}" must be refused`);
    assert.match(r.words, /percent/i);
  }
});

test('dollars are read through the decimal string into cents, never a float multiply', () => {
  assert.deepEqual(parseDiscountEntry('amount', '25'), { ok: true, discount: { amountCents: 2500 } });
  assert.deepEqual(parseDiscountEntry('amount', '19.68'), { ok: true, discount: { amountCents: 1968 } });
  assert.deepEqual(parseDiscountEntry('amount', '1.005'.slice(0, 4)), { ok: true, discount: { amountCents: 100 } });
  for (const bad of ['', '0', '0.00', '20.005', '-3', 'x', '$5']) {
    const r = parseDiscountEntry('amount', bad);
    assert.equal(r.ok, false, `"${bad}" must be refused`);
    assert.match(r.words, /dollar/i);
  }
});

test('the words say what came off and who approved it, in the server cents when known', () => {
  const money = (c) => `$${(c / 100).toFixed(2)}`;
  assert.equal(discountWords({ percentBps: 1000, ...approver }, 2000, money), '10% off ($20.00), approved by Sales manager');
  assert.equal(discountWords({ percentBps: 1250, ...approver }, undefined, money), '12.5% off, approved by Sales manager');
  assert.equal(discountWords({ amountCents: 2500, ...approver }, 2500, money), '$25.00 off, approved by Sales manager');
});

test('the wire shape is the contract body: one of percent or amount, and approved_by', () => {
  assert.deepEqual(discountWire({ percentBps: 1000, ...approver }), { percent_bps: 1000, approved_by: 'u-mgr' });
  assert.deepEqual(discountWire({ amountCents: 2500, ...approver }), { amount_cents: 2500, approved_by: 'u-mgr' });
  assert.equal(discountWire(undefined), undefined);
});

const facts = (over = {}) => ({
  branchId: 'b1', customerId: '', saleType: 'COLLECT_NOW', fulfilment: 'PICKUP',
  lines: [{ productId: 'p1', qty: 2, uom: 'SF' }], ...over,
});

test('the preview asks with the discounts, so the total shown is the total charged', () => {
  const plain = previewBody(facts());
  assert.equal('discount' in plain, false);
  assert.equal('discount' in plain.lines[0], false);
  const withLine = previewBody(facts({ lines: [{ productId: 'p1', qty: 2, uom: 'SF', discount: { percentBps: 1000, ...approver } }] }));
  assert.deepEqual(withLine.lines[0].discount, { percent_bps: 1000, approved_by: 'u-mgr' });
  const withTicket = previewBody(facts({ discount: { amountCents: 500, ...approver } }));
  assert.deepEqual(withTicket.discount, { amount_cents: 500, approved_by: 'u-mgr' });
  // Any change to a discount, its value or its approver, is a reason to ask again.
  const key = previewKey(withTicket);
  assert.notEqual(previewKey(previewBody(facts({ discount: { amountCents: 600, ...approver } }))), key);
  assert.notEqual(previewKey(previewBody(facts({ discount: { amountCents: 500, approvedBy: 'u-other', approvedByName: 'x' } }))), key);
  assert.notEqual(previewKey(plain), key);
});

test('the preview answer carries the server discount cents, per line, or none', () => {
  const plain = readPreview({ subtotal_cents: 400, tax_cents: 52, total_cents: 452 });
  assert.equal(plain.discountCents, 0);
  assert.deepEqual(plain.lines, []);
  const got = readPreview({
    subtotal_cents: 17100, tax_cents: 2223, total_cents: 19323, discount_cents: 2900, sale_discount_cents: 1900,
    lines: [{ line_no: 1, line_total_cents: 7300, discount_cents: 1000, sale_discount_cents: 900 }, { line_no: 2, line_total_cents: 9800, sale_discount_cents: 1000 }],
  });
  assert.equal(got.kind, 'ready');
  assert.equal(got.discountCents, 2900);
  assert.equal(got.saleDiscountCents, 1900);
  assert.deepEqual(got.lines, [
    { lineNo: 1, lineTotalCents: 7300, discountCents: 1000, saleDiscountCents: 900 },
    { lineNo: 2, lineTotalCents: 9800, discountCents: 0, saleDiscountCents: 1000 },
  ]);
  // A malformed discount figure is not an answer.
  assert.equal(readPreview({ subtotal_cents: 400, tax_cents: 52, total_cents: 452, discount_cents: 1.5 }).kind, 'unavailable');
});

test('a refusal in words is read for the total row, an unreadable one is just unavailable', () => {
  assert.deepEqual(readPreview({ error: 'line 1: a discount cannot be more than the line' }, 422), {
    kind: 'refused', words: 'line 1: a discount cannot be more than the line',
  });
  assert.equal(readPreview({}, 422).kind, 'unavailable');
  assert.equal(readPreview({ error: 'x' }, 500).kind, 'unavailable');
});

test('a line with a discount is the clerk\'s own work: it never merges and no pricing answer writes it', () => {
  assert.equal(LINE_FIELD_RULES.discount, 'block');
  assert.equal(PRICE_ANSWER_FIELDS.discount, 'operator');
  const line = { product: { id: 'p1' }, qty: 1, uom: 'SF', unitPriceCents: 100, priceUom: 'SF', lineTotalCents: 100, breakdown: null };
  assert.deepEqual(lineMergeBlockers(line), []);
  assert.deepEqual(lineMergeBlockers({ ...line, discount: { percentBps: 500, ...approver } }), ['discount']);
  const repriced = applyPriceAnswer({ ...line, discount: { percentBps: 500, ...approver } },
    { unitPriceCents: 120, priceUom: 'SF', lineTotalCents: 120, breakdown: null, priceMissing: false });
  assert.deepEqual(repriced.discount, { percentBps: 500, ...approver });
});

test('a ticket carries a discount when any line or the ticket does: that is what parking refuses', () => {
  const line = { product: { id: 'p1' }, qty: 1, uom: 'SF' };
  assert.equal(ticketCarriesDiscount([line], null), false);
  assert.equal(ticketCarriesDiscount([line, { ...line, discount: { amountCents: 5, ...approver } }], null), true);
  assert.equal(ticketCarriesDiscount([line], { percentBps: 100, ...approver }), true);
  assert.equal(discountFromLine(line), undefined);
});

// A manager's yes is for the line and quantity it was given (the review's P2-2): the discount
// carries what it was approved for, and the ticket drops it, in words, when that no longer holds.
import { discountsThatStand, ticketBasis, discountRemovedWords } from './_pos-discount.ts';

const line = (qty, total, discount) => ({ product: { id: 'p1' }, qty, uom: 'SF', lineTotalCents: total, ...(discount ? { discount } : {}) });
const yes = (over = {}) => ({ percentBps: 1000, approvedBy: 'u-mgr', approvedByName: 'Sales manager', approvedQty: 10, approvedBaseCents: 1000, ...over });

test('the wire carries the quantity and base the yes was given for', () => {
  assert.deepEqual(discountWire(yes()), { percent_bps: 1000, approved_by: 'u-mgr', approved_qty: 10, approved_base_cents: 1000 });
  assert.deepEqual(discountWire({ amountCents: 500, approvedBy: 'u', approvedByName: 'x', approvedBaseCents: 9000 }),
    { amount_cents: 500, approved_by: 'u', approved_base_cents: 9000 });
});

test('a line discount stands only at the quantity and line total it was approved at', () => {
  const same = discountsThatStand([line(10, 1000, yes())], null);
  assert.equal(same.cart[0].discount !== undefined, true);
  assert.equal(same.lineRemoved, false);
  const grew = discountsThatStand([line(100, 10000, yes())], null);
  assert.equal(grew.cart[0].discount, undefined);
  assert.equal(grew.lineRemoved, true);
  assert.equal(grew.reason, 'quantity');
  const repriced = discountsThatStand([line(10, 1200, yes())], null);
  assert.equal(repriced.cart[0].discount, undefined);
  assert.equal(repriced.reason, 'price');
  // A line still waiting on its price keeps its yes: nothing new is known yet.
  const pending = discountsThatStand([{ ...line(10, 1000, yes()), priceMissing: true }], null);
  assert.equal(pending.lineRemoved, false);
});

test('a ticket discount stands only while the ticket is the one it was approved on', () => {
  const cart = [line(10, 1000)];
  const t = { amountCents: 100, approvedBy: 'u', approvedByName: 'x', approvedBaseCents: 1000, basis: ticketBasis(cart) };
  assert.equal(discountsThatStand(cart, t).ticket !== null, true);
  const more = [...cart, line(1, 100)];
  const dropped = discountsThatStand(more, t);
  assert.equal(dropped.ticket, null);
  assert.equal(dropped.ticketRemoved, true);
  // A quantity change, a price change and a new line discount all change the basis.
  assert.notEqual(ticketBasis([line(11, 1000)]), ticketBasis(cart));
  assert.notEqual(ticketBasis([line(10, 900)]), ticketBasis(cart));
  assert.notEqual(ticketBasis([line(10, 1000, yes())]), ticketBasis(cart));
});

test('the words say what happened and what to do', () => {
  assert.equal(discountRemovedWords('quantity', false), 'The discount was removed because the quantity changed. Ask a manager again.');
  assert.equal(discountRemovedWords('price', false), 'The discount was removed because the price changed. Ask a manager again.');
  assert.equal(discountRemovedWords(null, true), 'The ticket discount was removed because the ticket changed. Ask a manager again.');
  assert.equal(discountRemovedWords('quantity', true),
    'The discount was removed because the quantity changed. The ticket discount was removed because the ticket changed. Ask a manager again.');
});
