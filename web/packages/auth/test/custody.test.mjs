// Session custody: the JWT lives in memory only, the mirror carries the
// display profile and nothing else (no token, no handle, no secret of any
// kind, in any storage), and the throw-safe mirror degrades observably when
// the storage accessor throws, the ERP shim's recorded finding carried with
// its rationale in src/session.ts.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SessionStore,
  ThrowSafeMirror,
  SESSION_MIRROR_KEY,
} from "../src/session.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  RecordedStorage,
  ThrowingStorage,
} from "../src/testing.ts";

const START_MS = 1_000_000_000_000;

function rig() {
  const recorded = new RecordedStorage();
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded);
  let nowMs = START_MS;
  const now = () => nowMs;
  const store = new SessionStore({ mirror, now });
  return {
    recorded,
    mirror,
    store,
    now,
    advance: (ms) => {
      nowMs += ms;
    },
    sessionInput: () => ({
      token: FAKE_JWT,
      tokenExpiresAtMs: nowMs + 60_000,
      user: { ...FAKE_ACCOUNT },
    }),
  };
}

test("adopt holds the token in memory and mirrors the display profile, never the token", () => {
  const { recorded, store, sessionInput } = rig();
  assert.equal(store.degraded, false, "a working accessor is not degraded");
  assert.equal(store.current, null, "no session before adopt");
  assert.equal(store.adopt(sessionInput()), true);
  assert.equal(store.current.token, FAKE_JWT, "the live token reads from memory");

  const writes = recorded.writes.filter((w) => w.key === SESSION_MIRROR_KEY);
  assert.equal(writes.length, 1, "exactly one mirrored payload");
  const payload = JSON.parse(writes[0].value);
  assert.deepEqual(
    Object.keys(payload).sort(),
    ["email", "name", "userId", "v"],
    "the mirrored payload carries the display profile and nothing else",
  );
  assert.equal("token" in payload, false, "no token field in the mirror");
  assert.equal("handle" in payload, false, "no handle field in the mirror: no refresh handle exists");
  for (const write of recorded.writes) {
    assert.equal(
      write.value.includes(FAKE_JWT),
      false,
      `no mirrored write carries the token (key ${write.key})`,
    );
  }
});

test("a reload restores the display profile and nothing else", () => {
  const { recorded, sessionInput } = rig();
  const first = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  first.adopt(sessionInput());

  const reloaded = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  assert.equal(reloaded.current, null, "a reload restores no token: none was ever stored");
  assert.deepEqual(reloaded.profile, { ...FAKE_ACCOUNT }, "a reload restores the display profile");
  const raw = recorded.writes.find((w) => w.key === SESSION_MIRROR_KEY).value;
  assert.equal(raw.includes("handle"), false, "the payload names no handle: nothing in storage trades for a session");
  assert.equal(raw.includes(FAKE_JWT), false, "the payload carries no token");
});

test("the custody holds nothing to trade: no storage write anywhere is bearer-capable", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  store.clear();
  store.adopt(sessionInput());
  for (const write of recorded.writes) {
    const payload = JSON.parse(write.value);
    for (const key of Object.keys(payload)) {
      assert.equal(
        /token|handle|secret|jwt/i.test(key),
        false,
        `the payload key ${key} is credential-shaped and must not be stored`,
      );
    }
  }
});

test("the store serialises to nothing secret, and the private fields are private at run time", () => {
  const { store, sessionInput } = rig();
  store.adopt(sessionInput());
  assert.equal(Object.keys(store).includes("token"), false, "the token is not an enumerable property");
  assert.equal(store.token, undefined, "the token is not readable as a property");
  assert.equal(store.mirror, undefined, "the mirror is not on the public surface");
  const printed = JSON.stringify(store);
  assert.equal(printed.includes(FAKE_JWT), false, "nothing secret serialises out of the store");
  const parsed = JSON.parse(printed);
  assert.deepEqual(
    Object.keys(parsed).sort(),
    ["degraded", "profile", "signedIn"],
    "the store serialises to the degradation flag, the live flag, and the display profile",
  );
  assert.equal(parsed.signedIn, true);
  assert.deepEqual(parsed.profile, { ...FAKE_ACCOUNT });
});

test("clear wipes memory and mirror", () => {
  const { recorded, store, sessionInput } = rig();
  store.adopt(sessionInput());
  store.clear();
  assert.equal(store.current, null);
  assert.equal(store.profile, null);
  assert.equal(recorded.size, 0, "the mirror holds nothing after clear");
});

test("an expired token reads as null while the display profile stays", () => {
  const { store, sessionInput, advance } = rig();
  store.adopt(sessionInput());
  assert.notEqual(store.current, null);
  advance(60_000);
  assert.equal(store.current, null, "an expired token is not a live session");
  assert.notEqual(store.profile, null, "the display profile is not a live session and not a credential");
});

test("adopt refuses unusable input and changes nothing", () => {
  const { recorded, store, sessionInput } = rig();
  const good = sessionInput();
  assert.equal(store.adopt({ ...good, token: "" }), false, "an empty token is refused");
  assert.equal(store.adopt({ ...good, tokenExpiresAtMs: START_MS - 1 }), false, "an expired token is refused");
  assert.equal(store.adopt({ ...good, user: { ...FAKE_ACCOUNT, userId: "" } }), false, "a user without an id is refused");
  assert.equal(store.current, null, "nothing was adopted");
  assert.equal(recorded.writes.some((w) => w.key === SESSION_MIRROR_KEY), false, "nothing was mirrored");
});

test("an unusable mirror payload is dropped from storage, not merely unread", () => {
  const cases = [
    "not json at all",
    // a version 1 payload carried a refresh handle; the rework stores no
    // credential of any kind, so a leftover v1 payload is refused and wiped
    JSON.stringify({ v: 1, handle: "leftover-handle", handleExpiresAtMs: START_MS + 1000, userId: "u", email: "x", name: "y" }),
    JSON.stringify({ v: 2, userId: "", email: "x", name: "y" }),
    JSON.stringify({ v: 2, email: "x", name: "y" }),
    JSON.stringify({ v: 3, userId: "u", email: "x", name: "y" }),
  ];
  for (const raw of cases) {
    const recorded = new RecordedStorage();
    recorded.setItem(SESSION_MIRROR_KEY, raw);
    const store = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
    assert.equal(store.profile, null, `the payload ${raw} reads as absent`);
    assert.equal(
      recorded.size,
      0,
      `the payload ${raw} was dropped from storage, not kept where a later reader could trust it`,
    );
  }
});

test("the mirror degrades observably on a throwing accessor, and a degraded reload restores nothing", () => {
  const throwing = () => new ThrowSafeMirror(SESSION_MIRROR_KEY, new ThrowingStorage());
  const mirror = throwing();
  const store = new SessionStore({ mirror, now: () => START_MS });
  assert.equal(store.degraded, true, "the degradation is observable");
  store.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: START_MS + 60_000,
    user: { ...FAKE_ACCOUNT },
  });
  assert.notEqual(store.current, null, "custody works in a degraded browser: sign-in still works");
  assert.notEqual(store.profile, null, "within the process the display profile still reads");

  const reloaded = new SessionStore({ mirror: throwing(), now: () => START_MS });
  assert.equal(reloaded.degraded, true);
  assert.equal(reloaded.profile, null, "a degraded reload restores nothing, which is the honest posture");
});

test("the default resolution degrades where no browser storage exists (the Node test process)", () => {
  const store = new SessionStore();
  assert.equal(store.degraded, true);
});

test("mirror writes are throw-safe: a serialisation failure returns false and throws nothing", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  const circular = {};
  circular.self = circular;
  assert.equal(mirror.write(circular), false, "an unserialisable payload is refused as data");
  assert.equal(mirror.write({ v: 2 }), true, "a plain payload writes");
});

test("mirror reads are throw-safe and validated rather than cast", () => {
  const mirror = new ThrowSafeMirror(SESSION_MIRROR_KEY, new RecordedStorage());
  assert.equal(mirror.read(), undefined, "an empty mirror reads as undefined");
  mirror.write({ v: 2 });
  assert.deepEqual(mirror.read(), { v: 2 });
  mirror.clear();
  assert.equal(mirror.read(), undefined);
});
