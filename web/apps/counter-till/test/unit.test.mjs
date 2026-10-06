// The one time credential slot's own unit (the review round's P2-2): the
// slot is the only state the approver's token rests in between its mint and
// its one send, so its one-time-ness is asserted directly. A second take
// answers nothing, a drop empties it whatever it held, and an empty slot
// takes as nothing, because the header provider's take is what dies the
// token and a second resolution of it must never send again.
import { test } from "node:test";
import assert from "node:assert/strict";
import { OneTimeCredential } from "../src/returns/approval.js";
import { clearFrontDoorHandoff, FRONT_DOOR_SESSION_KEY, readFrontDoorHandoff } from "../src/demo-account.js";

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

// The front door's handoff read (HH-DEMO-1 D2 polish, item 6): the till's
// boot takes the front door's session key only when the record validates
// whole, and an unusable or expired record is dropped, never adopted. The
// storage double is injectable, so nothing here touches a browser.

function handoffStorage() {
  const mem = new Map();
  return {
    getItem: (key) => (mem.has(key) ? mem.get(key) : null),
    setItem: (key, value) => {
      mem.set(key, value);
    },
    removeItem: (key) => {
      mem.delete(key);
    },
  };
}

const NOW = 1_700_000_000_000;

function handoffRecord(overrides = {}) {
  return {
    token: "header.payload.signature",
    tokenExpiresAtMs: NOW + 3_600_000,
    flow: "sales",
    roleLabel: "Sales",
    user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
    roleCode: "inside_sales",
    roleName: "Inside Sales",
    tier: 3,
    scope: "BRANCH",
    ...overrides,
  };
}

test("the front door's handoff reads ready when the record validates whole", () => {
  const storage = handoffStorage();
  assert.equal(readFrontDoorHandoff({ storage, now: () => NOW }).kind, "absent", "no record is absent");
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(handoffRecord()));
  const read = readFrontDoorHandoff({ storage, now: () => NOW });
  assert.equal(read.kind, "ready");
  assert.equal(read.session.flow, "sales");
  assert.equal(read.session.user.userId, "demo-account-sales");
  assert.equal(read.session.tier, 3);
});

test("a corrupt, short or expired handoff record is unusable, never a session", () => {
  const storage = handoffStorage();
  storage.setItem(FRONT_DOOR_SESSION_KEY, "{not json");
  assert.equal(
    readFrontDoorHandoff({ storage, now: () => NOW }).kind,
    "absent",
    "not json reads as absent: the mirror answers undefined for an unreadable record",
  );
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify({ token: "x" }));
  assert.equal(readFrontDoorHandoff({ storage, now: () => NOW }).kind, "unusable", "a record with one field");
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(handoffRecord({ tokenExpiresAtMs: NOW - 1 })));
  assert.equal(readFrontDoorHandoff({ storage, now: () => NOW }).kind, "unusable", "an expired mint is not a session");
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(handoffRecord({ tier: "three" })));
  assert.equal(readFrontDoorHandoff({ storage, now: () => NOW }).kind, "unusable", "a tier that is not a number");
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(handoffRecord({ user: { userId: "u" } })));
  assert.equal(readFrontDoorHandoff({ storage, now: () => NOW }).kind, "unusable", "a short user block");
});

test("the handoff clear drops the key and nothing else", () => {
  const storage = handoffStorage();
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(handoffRecord()));
  storage.setItem("hh-auth.session.v2", JSON.stringify({ v: 2, userId: "u", email: "e", name: "n" }));
  clearFrontDoorHandoff({ storage });
  assert.equal(storage.getItem(FRONT_DOOR_SESSION_KEY), null, "the handoff key is gone");
  assert.notEqual(storage.getItem("hh-auth.session.v2"), null, "the custody mirror is not touched");
  clearFrontDoorHandoff({ storage });
  assert.equal(storage.getItem(FRONT_DOOR_SESSION_KEY), null, "a second clear is quiet");
});
