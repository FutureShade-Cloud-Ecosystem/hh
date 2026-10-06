// Packet E, leg 1: the key is cleared after a 201. A successful card sale is
// the one answer the till already cleared the per-till card key on (the
// committed arm in pos-terminal.ts); the rule now lives beside the other card
// charge answers in cardChargeAnswer (_pos.ts), so the whole table is
// unit-pinned and no arm can drift from it. The successful answer keeps the
// confirmation panel's own words, so the wording half is empty here on
// purpose.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cardChargeAnswer } from './_pos.ts';

test('a 201 card sale answer clears the per-till card key and adds no words of its own', () => {
  assert.deepEqual(cardChargeAnswer(201, '', false), { msg: '', clearsKey: true });
  assert.deepEqual(cardChargeAnswer(201, '', true), { msg: '', clearsKey: true });
});
