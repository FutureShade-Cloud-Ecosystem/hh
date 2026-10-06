// Packet E, leg 2: the key is cleared on the 402 card_charge_card_declined
// answer and the till shows the standard decline copy (the single tender's
// ring). On main the till keeps the key on a decline, so a kept key after a
// decline would answer "declined" forever; the rule table pins the clear on
// this answer beside the words.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cardChargeAnswer } from './_pos.ts';

test('a 402 card_charge_card_declined answer on the single ring clears the key and shows the standard decline copy', () => {
  assert.deepEqual(cardChargeAnswer(402, 'card_charge_card_declined', false), {
    msg: 'Card declined: try another tender.',
    clearsKey: true,
  });
});
