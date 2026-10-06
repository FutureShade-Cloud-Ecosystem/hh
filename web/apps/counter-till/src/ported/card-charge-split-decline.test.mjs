// Packet E, leg 5: on the split decline answer (leg 1 succeeded, leg 2
// declined) the till clears the key and shows the split decline copy: what
// happened to the declined leg, what became of the card money already taken,
// and the way out. The server reversed every captured leg before answering,
// so the copy may promise the reversal.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cardChargeAnswer } from './_pos.ts';

test('the split decline answer clears the key and shows the split decline copy', () => {
  assert.deepEqual(cardChargeAnswer(402, 'card_charge_card_declined', true), {
    msg: 'A card leg was declined. Any card already charged for this sale was reversed. Try another tender.',
    clearsKey: true,
  });
});
