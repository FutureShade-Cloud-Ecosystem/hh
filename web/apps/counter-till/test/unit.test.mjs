// The one time credential slot's own unit (the review round's P2-2): the
// slot is the only state the approver's token rests in between its mint and
// its one send, so its one-time-ness is asserted directly. A second take
// answers nothing, a drop empties it whatever it held, and an empty slot
// takes as nothing, because the header provider's take is what dies the
// token and a second resolution of it must never send again.
import { test } from "node:test";
import assert from "node:assert/strict";
import { OneTimeCredential } from "../src/returns/approval.js";

test("the one time slot answers its token once, and nothing after", () => {
  const slot = new OneTimeCredential();
  assert.equal(slot.take(), null, "an empty slot takes as nothing");
  slot.set("token-a");
  assert.equal(slot.take(), "token-a", "the first take carries the token");
  assert.equal(slot.take(), null, "the second take answers nothing");
  assert.equal(slot.take(), null, "and keeps answering nothing");
});

test("the slot's drop empties it whatever it held", () => {
  const slot = new OneTimeCredential();
  slot.set("token-b");
  slot.drop();
  assert.equal(slot.take(), null);
  slot.drop();
  assert.equal(slot.take(), null);
});
