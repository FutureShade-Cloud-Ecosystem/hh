// Packet E, leg 4: the key is cleared on the 409 card_charge_needs_manager
// answer (a charge past grace with no answer from the processor) and the till
// shows the needs-manager copy. The answer body's attempts list is the
// manager's diagnostics; it never rides the cashier's words.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cardChargeAnswer } from './_pos.ts';

test('a 409 card_charge_needs_manager answer clears the key and shows the needs-manager copy', () => {
  assert.deepEqual(cardChargeAnswer(409, 'card_charge_needs_manager', false), {
    msg: "This sale's card charge has no answer from the card processor. A manager has to check it before the card is charged again.",
    clearsKey: true,
  });
});
