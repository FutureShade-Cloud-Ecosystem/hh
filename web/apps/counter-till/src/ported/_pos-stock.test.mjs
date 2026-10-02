// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-stock.test.mjs; adapted for HH.

// DIB-488: the stock-status line decision table (_pos-stock.ts). Node built-in runner:
// node --import./src/_ts-js-loader.mjs --test 'src/**/*.test.mjs' (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { stockStatus, availableInSellUnit, formatQty } from './_pos-stock.ts';

const facts = (over = {}) => ({
  availableBase: undefined,
  lowBase: undefined,
  baseUom: 'EA',
  sellUom: 'EA',
  sellFactor: 1,
  ...over,
});

test('DIB-488: no glance answer renders NOTHING: absent data is not an error and not a claim', () => {
  assert.equal(stockStatus(facts()), null);
});

test('DIB-488: zero or negative available is out of stock', () => {
  assert.deepEqual(stockStatus(facts({ availableBase: 0 })), {
    tone: 'out',
    label: 'Out of stock at this branch',
  });
  assert.equal(stockStatus(facts({ availableBase: -3 })).tone, 'out');
});

test('DIB-488: in stock speaks the quantity and unit', () => {
  assert.deepEqual(stockStatus(facts({ availableBase: 250, baseUom: 'SF', sellUom: 'SF' })), {
    tone: 'ok',
    label: 'In stock · 250 SF available',
  });
});

test('DIB-488: low ONLY where the dealer authored a threshold: no invented cutoffs', () => {
 // Same availability, no threshold on file → in stock, full stop.
  assert.equal(stockStatus(facts({ availableBase: 4 })).tone, 'ok');
 // With an authored reorder point at/above the availability → low.
  assert.deepEqual(stockStatus(facts({ availableBase: 4, lowBase: 10, baseUom: 'T', sellUom: 'T' })), {
    tone: 'low',
    label: 'Low stock · 4 T available',
  });
 // Above the threshold → back to in stock.
  assert.equal(stockStatus(facts({ availableBase: 11, lowBase: 10 })).tone, 'ok');
});

test('DIB-488: sell-unit conversion divides by the on-file factor (base 4.1/SF → SF spoken)', () => {
  const st = stockStatus(facts({ availableBase: 205, baseUom: 'PC', sellUom: 'SF', sellFactor: 4.1 }));
  assert.equal(st.tone, 'ok');
  assert.equal(st.label, 'In stock · 50 SF available');
});

test('DIB-488: NO conversion on file → the base unit is spoken honestly, never divided by a guess', () => {
  const st = stockStatus(facts({ availableBase: 30, baseUom: 'PC', sellUom: 'SF', sellFactor: 0 }));
  assert.equal(st.label, 'In stock · 30 PC available');
  assert.equal(availableInSellUnit(30, 0), null);
  assert.equal(availableInSellUnit(30, -1), null);
  assert.equal(availableInSellUnit(41, 4.1), 10);
});

test('DIB-488: the LOW decision is made in BASE units even when the label speaks sell units', () => {
 // 205 PC available, threshold 300 PC: low; the label still speaks SF.
  const st = stockStatus(facts({ availableBase: 205, lowBase: 300, baseUom: 'PC', sellUom: 'SF', sellFactor: 4.1 }));
  assert.equal(st.tone, 'low');
  assert.equal(st.label, 'Low stock · 50 SF available');
});

test('DIB-488: quantities read like a counter, not a ledger', () => {
  assert.equal(formatQty(250), '250');
  assert.equal(formatQty(2.5), '2.5');
  assert.equal(formatQty(2.04), '2');
  assert.equal(formatQty(0.25), '0.3');
});
