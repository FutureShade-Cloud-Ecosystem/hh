// Packet E, leg 3: the key is cleared on the 409 card_charge_in_flight answer
// (the same-key double submit's loser) and the till shows the in-flight copy.
// The word is honest either way: the server's unique leg key settled the race,
// so a second press cannot charge twice no matter which key it carries.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cardChargeAnswer } from './_pos.ts';

test('a 409 card_charge_in_flight answer clears the key and shows the in-flight copy', () => {
  assert.deepEqual(cardChargeAnswer(409, 'card_charge_in_flight', false), {
    msg: 'The card is still being charged. Wait a moment, then press Complete again: it will not charge twice.',
    clearsKey: true,
  });
});
